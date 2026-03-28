package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/orchestrate"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
)

const PlanSummaryPrompt = `You are summarizing the results of a multi-step task execution. Synthesize the outputs from all completed steps into a coherent, concise response for the user.

For each step result provided, note:
- What was accomplished
- Any errors or issues encountered
- Key outputs or artifacts produced

Provide a unified summary that directly addresses the original user request.`

// ToolAccessLevel defines what tools a dynamic agent can access.
type ToolAccessLevel string

const (
	ToolAccessFull     ToolAccessLevel = "full"      // bash, read, write, edit, glob, grep
	ToolAccessReadOnly ToolAccessLevel = "read-only" // read, glob, grep
	ToolAccessNone     ToolAccessLevel = "none"      // no tools (chat-only)
)

// validToolAccessLevels contains all valid ToolAccessLevel values.
var validToolAccessLevels = map[ToolAccessLevel]bool{
	ToolAccessFull:     true,
	ToolAccessReadOnly: true,
	ToolAccessNone:     true,
}

// validBaseTypes contains all valid base types for dynamic agents.
var validBaseTypes = map[string]bool{
	"coder":      true,
	"researcher": true,
	"reviewer":   true,
	"chat":       true,
}

// defaultBaseTypeToolAccess maps base types to their default ToolAccessLevel.
// "reviewer" is a special case handled separately (uses reviewReg).
var defaultBaseTypeToolAccess = map[string]ToolAccessLevel{
	"coder":      ToolAccessFull,
	"researcher": ToolAccessReadOnly,
	"chat":       ToolAccessNone,
}

// defaultBaseTypePrompt maps base types to their default system prompts.
var defaultBaseTypePrompt = map[string]string{
	"coder":      CoderSystemPrompt,
	"researcher": ResearcherSystemPrompt,
	"reviewer":   ReviewerSystemPrompt,
	"chat":       ChatSystemPrompt,
}

// DynamicAgentSpec defines the configuration for a dynamically created sub-agent.
type DynamicAgentSpec struct {
	BaseType     string          `json:"base_type"`               // required: coder, researcher, reviewer, chat
	SystemPrompt string          `json:"system_prompt,omitempty"` // optional: custom system prompt
	ToolAccess   ToolAccessLevel `json:"tool_access,omitempty"`   // optional: overrides base type default
	Model        string          `json:"model,omitempty"`         // optional: overrides configured model
}

// validate checks that a DynamicAgentSpec is well-formed.
func (s *DynamicAgentSpec) validate() error {
	if s.BaseType == "" {
		return fmt.Errorf("dynamic_spec: base_type is required")
	}
	if !validBaseTypes[s.BaseType] {
		return fmt.Errorf("dynamic_spec: invalid base_type %q (valid: coder, researcher, reviewer, chat)", s.BaseType)
	}
	if s.ToolAccess != "" && !validToolAccessLevels[s.ToolAccess] {
		return fmt.Errorf("dynamic_spec: invalid tool_access %q (valid: full, read-only, none)", s.ToolAccess)
	}
	return nil
}

// OrchestratorAgent is the main agent that receives all user requests.
// It coordinates explorer and planner sub-agents, then dispatches to
// the appropriate task agents (coder, researcher, reviewer, chat).
type OrchestratorAgent struct {
	agent.Base
	llm            aimodel.ChatCompleter
	model          string
	subAgents      map[string]agent.Agent // coder, researcher, reviewer, chat
	planGen        *taskagent.Agent       // LLM agent for plan summarization
	maxConcurrency int
	fallbackAgent  agent.Agent // chat agent as fallback
	workingDir     string      // captured CWD

	explorerAgent  agent.Agent                           // explores codebase to build context (nil if not configured)
	plannerAgent   agent.Agent                           // classifies/plans tasks
	toolRegistries map[ToolAccessLevel]tool.ToolRegistry // tool registries for dynamic agents (may be nil)
	reviewReg      tool.ToolRegistry                     // reviewer registry (not exposed as ToolAccessLevel)
	maxIterations  int                                   // from config, for dynamic agents
	runTokenBudget int                                   // from config, for dynamic agents (0 = unlimited)
}

// Compile-time interface checks.
var (
	_ agent.Agent            = (*OrchestratorAgent)(nil)
	_ agent.StreamAgent      = (*OrchestratorAgent)(nil)
	_ orchestrate.Aggregator = (*PlanAggregator)(nil)
)

// NewOrchestratorAgent creates a new OrchestratorAgent.
func NewOrchestratorAgent(
	cfg agent.Config,
	llm aimodel.ChatCompleter,
	model string,
	subAgents map[string]agent.Agent,
	planGen *taskagent.Agent,
	maxConcurrency int,
	fallback agent.Agent,
	workingDir string,
	explorerAgent agent.Agent,
	plannerAgent agent.Agent,
	toolRegistries map[ToolAccessLevel]tool.ToolRegistry,
	reviewReg tool.ToolRegistry,
	maxIterations int,
	runTokenBudget int,
) *OrchestratorAgent {
	return &OrchestratorAgent{
		Base:           agent.NewBase(cfg),
		llm:            llm,
		model:          model,
		subAgents:      subAgents,
		planGen:        planGen,
		maxConcurrency: maxConcurrency,
		fallbackAgent:  fallback,
		workingDir:     workingDir,
		explorerAgent:  explorerAgent,
		plannerAgent:   plannerAgent,
		toolRegistries: toolRegistries,
		reviewReg:      reviewReg,
		maxIterations:  maxIterations,
		runTokenBudget: runTokenBudget,
	}
}

// ClassifyResult is the structured LLM response for task classification.
type ClassifyResult struct {
	Mode  string `json:"mode"`  // "direct" or "plan"
	Agent string `json:"agent"` // only for mode="direct": coder/researcher/reviewer/chat
	Plan  *Plan  `json:"plan"`  // only for mode="plan"
}

// validate checks that the classification result references valid agents.
func (cr *ClassifyResult) validate(subAgents map[string]agent.Agent) error {
	switch cr.Mode {
	case "direct":
		if _, ok := subAgents[cr.Agent]; !ok {
			return fmt.Errorf("unknown agent %q in direct dispatch", cr.Agent)
		}
	case "plan":
		if cr.Plan == nil || len(cr.Plan.Steps) == 0 {
			return fmt.Errorf("plan mode but no steps provided")
		}
		for _, step := range cr.Plan.Steps {
			if step.DynamicSpec != nil {
				if err := step.DynamicSpec.validate(); err != nil {
					return fmt.Errorf("plan step %q: %w", step.ID, err)
				}
				if step.Agent != step.DynamicSpec.BaseType {
					return fmt.Errorf("plan step %q: agent %q must match dynamic_spec base_type %q", step.ID, step.Agent, step.DynamicSpec.BaseType)
				}
			} else {
				if _, ok := subAgents[step.Agent]; !ok {
					return fmt.Errorf("unknown agent %q in plan step %q", step.Agent, step.ID)
				}
			}
		}
	default:
		return fmt.Errorf("unknown classification mode %q", cr.Mode)
	}
	return nil
}

// Run executes the orchestrator: explores context, plans the task, then dispatches.
func (o *OrchestratorAgent) Run(ctx context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	// Phase 1: Explore project context.
	contextSummary, exploreUsage := o.explore(ctx, req)

	// Phase 2: Plan/classify the task.
	result, planUsage, err := o.planTask(ctx, req, contextSummary)
	if err != nil {
		slog.Warn("orchestrator: planning failed, falling back to chat", "error", err)
		return o.fallbackRun(ctx, req, aggregateUsage(exploreUsage, nil))
	}

	totalUsage := aggregateUsage(exploreUsage, planUsage)

	// Phase 3: Dispatch.
	enrichedReq := o.enrichRequest(req, contextSummary)

	switch result.Mode {
	case "direct":
		return o.runDirect(ctx, enrichedReq, result, totalUsage)
	case "plan":
		return o.runPlan(ctx, enrichedReq, result.Plan, totalUsage, contextSummary)
	default:
		slog.Warn("orchestrator: unknown mode, falling back to chat", "mode", result.Mode)
		return o.fallbackRun(ctx, enrichedReq, totalUsage)
	}
}

// RunStream implements streaming dispatch.
func (o *OrchestratorAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	// Phase 1: Explore project context.
	contextSummary, exploreUsage := o.explore(ctx, req)

	// Phase 2: Plan/classify the task.
	result, planUsage, err := o.planTask(ctx, req, contextSummary)
	if err != nil {
		slog.Warn("orchestrator: planning failed, falling back to chat stream", "error", err)
		return o.fallbackRunStream(ctx, req)
	}

	totalUsage := aggregateUsage(exploreUsage, planUsage)
	enrichedReq := o.enrichRequest(req, contextSummary)

	switch result.Mode {
	case "direct":
		subAgent, ok := o.subAgents[result.Agent]
		if !ok {
			return o.fallbackRunStream(ctx, enrichedReq)
		}
		sa, ok := subAgent.(agent.StreamAgent)
		if !ok {
			return agent.RunToStream(ctx, subAgent, enrichedReq), nil
		}
		return sa.RunStream(ctx, enrichedReq)
	case "plan":
		totalUsageCopy := totalUsage
		ctxSummary := contextSummary
		planRunner := agent.NewCustomAgent(
			agent.Config{ID: o.ID(), Name: o.Name(), Description: o.Description()},
			func(ctx context.Context, innerReq *schema.RunRequest) (*schema.RunResponse, error) {
				return o.runPlan(ctx, innerReq, result.Plan, totalUsageCopy, ctxSummary)
			},
		)
		return agent.RunToStream(ctx, planRunner, enrichedReq), nil
	default:
		return o.fallbackRunStream(ctx, enrichedReq)
	}
}

// explore calls the explorer sub-agent to build project context.
// Returns the context summary text and usage. If no explorer is configured
// or exploration fails, returns empty summary.
func (o *OrchestratorAgent) explore(ctx context.Context, req *schema.RunRequest) (string, *aimodel.Usage) {
	if o.explorerAgent == nil {
		return "", nil
	}

	// Build explorer request with working directory context.
	var msgs []schema.Message
	if o.workingDir != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Working directory: %s", o.workingDir),
		))
	}
	msgs = append(msgs, req.Messages...)

	explorerReq := &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
	}

	resp, err := o.explorerAgent.Run(ctx, explorerReq)
	if err != nil {
		slog.Warn("orchestrator: explorer failed", "error", err)
		return "", nil
	}

	if len(resp.Messages) == 0 {
		return "", resp.Usage
	}

	return resp.Messages[0].Content.Text(), resp.Usage
}

// planTask calls the planner sub-agent to classify the request.
func (o *OrchestratorAgent) planTask(ctx context.Context, req *schema.RunRequest, contextSummary string) (*ClassifyResult, *aimodel.Usage, error) {
	if o.plannerAgent == nil {
		return o.classifyTaskDirect(ctx, req)
	}

	// Build planner request with context.
	var msgs []schema.Message
	if o.workingDir != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Working directory: %s", o.workingDir),
		))
	}
	if contextSummary != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Project context:\n%s", contextSummary),
		))
	}
	msgs = append(msgs, req.Messages...)

	plannerReq := &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
	}

	resp, err := o.plannerAgent.Run(ctx, plannerReq)
	if err != nil {
		return nil, nil, fmt.Errorf("planner run: %w", err)
	}

	usage := resp.Usage

	if len(resp.Messages) == 0 {
		return nil, usage, fmt.Errorf("empty planner response")
	}

	text := resp.Messages[0].Content.Text()
	jsonStr := extractJSON(text)

	var result ClassifyResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, usage, fmt.Errorf("parse planner JSON: %w", err)
	}

	if err := result.validate(o.subAgents); err != nil {
		return nil, usage, err
	}

	return &result, usage, nil
}

// classifyTaskDirect makes a direct LLM call to classify the task (fallback when no planner agent).
func (o *OrchestratorAgent) classifyTaskDirect(ctx context.Context, req *schema.RunRequest) (*ClassifyResult, *aimodel.Usage, error) {
	systemPrompt := strings.Replace(PlannerSystemPrompt, "{{.WorkingDir}}", o.workingDir, 1)

	msgs := make([]aimodel.Message, 0, len(req.Messages)+1)
	msgs = append(msgs, aimodel.Message{
		Role:    aimodel.RoleSystem,
		Content: aimodel.NewTextContent(systemPrompt),
	})
	msgs = append(msgs, schema.ToAIModelMessages(req.Messages)...)

	chatReq := &aimodel.ChatRequest{
		Model:    o.model,
		Messages: msgs,
	}

	resp, err := o.llm.ChatCompletion(ctx, chatReq)
	if err != nil {
		return nil, nil, err
	}

	usage := &resp.Usage

	if len(resp.Choices) == 0 {
		return nil, usage, fmt.Errorf("empty classification response")
	}
	text := resp.Choices[0].Message.Content.Text()

	jsonStr := extractJSON(text)

	var result ClassifyResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, usage, fmt.Errorf("parse classification JSON: %w", err)
	}

	if err := result.validate(o.subAgents); err != nil {
		return nil, usage, err
	}

	return &result, usage, nil
}

// enrichRequest prepends working directory and exploration context to a request for sub-agent dispatch.
func (o *OrchestratorAgent) enrichRequest(req *schema.RunRequest, contextSummary string) *schema.RunRequest {
	if o.workingDir == "" && contextSummary == "" {
		return req
	}
	var msgs []schema.Message
	if o.workingDir != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Working directory: %s", o.workingDir),
		))
	}
	if contextSummary != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Project context:\n%s", contextSummary),
		))
	}
	msgs = append(msgs, req.Messages...)
	return &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
		Options:   req.Options,
		Metadata:  req.Metadata,
	}
}

// runDirect dispatches to a single sub-agent and aggregates usage.
func (o *OrchestratorAgent) runDirect(ctx context.Context, req *schema.RunRequest, cr *ClassifyResult, classifyUsage *aimodel.Usage) (*schema.RunResponse, error) {
	subAgent, ok := o.subAgents[cr.Agent]
	if !ok {
		return o.fallbackRun(ctx, req, classifyUsage)
	}

	resp, err := subAgent.Run(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: sub-agent %q failed: %w", cr.Agent, err)
	}

	resp.Usage = aggregateUsage(classifyUsage, resp.Usage)
	return resp, nil
}

// runPlan builds and executes a DAG from the plan.
func (o *OrchestratorAgent) runPlan(ctx context.Context, req *schema.RunRequest, plan *Plan, classifyUsage *aimodel.Usage, contextSummary string) (*schema.RunResponse, error) {
	nodes, err := o.buildNodes(plan, req, contextSummary)
	if err != nil {
		slog.Warn("orchestrator: DAG build failed, falling back to chat", "error", err)
		return o.fallbackRun(ctx, req, classifyUsage)
	}

	dagCfg := orchestrate.DAGConfig{
		MaxConcurrency: o.maxConcurrency,
		ErrorStrategy:  orchestrate.Skip,
		Aggregator:     &PlanAggregator{summarizer: o.planGen},
	}

	result, err := orchestrate.ExecuteDAG(ctx, dagCfg, nodes, req)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: DAG execution failed: %w", err)
	}

	resp := result.FinalOutput
	if resp == nil {
		resp = &schema.RunResponse{}
	}
	resp.Usage = aggregateUsage(classifyUsage, resp.Usage)
	return resp, nil
}

// fallbackRun delegates to the fallback agent with a warning prepended.
func (o *OrchestratorAgent) fallbackRun(ctx context.Context, req *schema.RunRequest, classifyUsage *aimodel.Usage) (*schema.RunResponse, error) {
	if o.fallbackAgent == nil {
		return nil, fmt.Errorf("orchestrator: no fallback agent available")
	}

	msgs := make([]schema.Message, 0, len(req.Messages)+1)
	msgs = append(msgs, schema.NewUserMessage("Note: task classification failed, executing as a general conversation."))
	msgs = append(msgs, req.Messages...)

	fallbackReq := &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
		Options:   req.Options,
		Metadata:  req.Metadata,
	}

	resp, err := o.fallbackAgent.Run(ctx, fallbackReq)
	if err != nil {
		return nil, err
	}

	resp.Usage = aggregateUsage(classifyUsage, resp.Usage)
	return resp, nil
}

// fallbackRunStream returns a stream from the fallback agent.
func (o *OrchestratorAgent) fallbackRunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	if o.fallbackAgent == nil {
		return nil, fmt.Errorf("orchestrator: no fallback agent available")
	}
	sa, ok := o.fallbackAgent.(agent.StreamAgent)
	if !ok {
		return agent.RunToStream(ctx, o.fallbackAgent, req), nil
	}
	return sa.RunStream(ctx, req)
}

// aggregateUsage merges two usage structs into a single Usage.
func aggregateUsage(a *aimodel.Usage, b *aimodel.Usage) *aimodel.Usage {
	if a == nil && b == nil {
		return nil
	}
	result := &aimodel.Usage{}
	if a != nil {
		result.Add(a)
	}
	if b != nil {
		result.Add(b)
	}
	return result
}

// Plan represents a parsed execution plan from the LLM.
type Plan struct {
	Goal  string     `json:"goal"`
	Steps []PlanStep `json:"steps"`
}

// PlanStep represents a single step in the plan.
type PlanStep struct {
	ID          string            `json:"id"`
	Description string            `json:"description"`
	Agent       string            `json:"agent"`
	DependsOn   []string          `json:"depends_on"`
	DynamicSpec *DynamicAgentSpec `json:"dynamic_spec,omitempty"` // optional dynamic agent specification
}

// extractJSON attempts to extract a JSON object from text that may contain
// markdown code fences or other surrounding text.
func extractJSON(text string) string {
	// Try to find JSON within markdown code fences.
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + len("```json")
		if end := strings.Index(text[start:], "```"); end >= 0 {
			return strings.TrimSpace(text[start : start+end])
		}
	}

	if idx := strings.Index(text, "```"); idx >= 0 {
		start := idx + len("```")
		if end := strings.Index(text[start:], "```"); end >= 0 {
			candidate := strings.TrimSpace(text[start : start+end])
			if strings.HasPrefix(candidate, "{") {
				return candidate
			}
		}
	}

	// Try to find a JSON object directly, skipping braces inside quoted strings.
	if start := strings.Index(text, "{"); start >= 0 {
		depth := 0
		inString := false
		for i := start; i < len(text); i++ {
			ch := text[i]
			if inString {
				if ch == '\\' && i+1 < len(text) {
					i++ // skip escaped character
					continue
				}
				if ch == '"' {
					inString = false
				}
				continue
			}
			switch ch {
			case '"':
				inString = true
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return text[start : i+1]
				}
			}
		}
	}

	return text
}

// buildDynamicAgent creates an ephemeral taskagent from a DynamicAgentSpec.
// Returns an error if the spec references unavailable registries or the
// orchestrator was not configured with tool registries.
func (o *OrchestratorAgent) buildDynamicAgent(stepID string, spec *DynamicAgentSpec) (*taskagent.Agent, error) {
	// Determine tool access level.
	var registry tool.ToolRegistry
	if spec.ToolAccess != "" {
		// Explicit tool access override.
		if spec.ToolAccess == ToolAccessNone {
			registry = nil
		} else {
			if o.toolRegistries == nil {
				return nil, fmt.Errorf("tool registries not configured for dynamic agents")
			}
			reg, ok := o.toolRegistries[spec.ToolAccess]
			if !ok {
				return nil, fmt.Errorf("no tool registry for access level %q", spec.ToolAccess)
			}
			registry = reg
		}
	} else {
		// Derive from base type.
		if spec.BaseType == "reviewer" {
			// Reviewer uses its own special registry.
			registry = o.reviewReg
		} else if defaultAccess, ok := defaultBaseTypeToolAccess[spec.BaseType]; ok {
			if defaultAccess == ToolAccessNone {
				registry = nil
			} else {
				if o.toolRegistries == nil {
					return nil, fmt.Errorf("tool registries not configured for dynamic agents")
				}
				reg, ok := o.toolRegistries[defaultAccess]
				if !ok {
					return nil, fmt.Errorf("no tool registry for default access level %q of base type %q", defaultAccess, spec.BaseType)
				}
				registry = reg
			}
		}
	}

	// Determine system prompt.
	systemPrompt := spec.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = defaultBaseTypePrompt[spec.BaseType]
	}

	// Determine model.
	model := spec.Model
	if model == "" {
		model = o.model
	}

	// Determine max iterations.
	maxIter := o.maxIterations
	if maxIter == 0 {
		maxIter = 10 // sensible default
	}

	var opts []taskagent.Option
	opts = append(opts,
		taskagent.WithChatCompleter(o.llm),
		taskagent.WithModel(model),
		taskagent.WithSystemPrompt(prompt.StringPrompt(systemPrompt)),
		taskagent.WithMaxIterations(maxIter),
	)
	if registry != nil {
		opts = append(opts, taskagent.WithToolRegistry(registry))
	}
	if o.runTokenBudget > 0 {
		opts = append(opts, taskagent.WithRunTokenBudget(o.runTokenBudget))
	}

	return taskagent.New(
		agent.Config{
			ID:          fmt.Sprintf("dynamic_%s_%s", spec.BaseType, stepID),
			Name:        fmt.Sprintf("Dynamic %s Agent (%s)", spec.BaseType, stepID),
			Description: fmt.Sprintf("Dynamically created %s agent for step %s", spec.BaseType, stepID),
		},
		opts...,
	), nil
}

// buildNodes converts a Plan into orchestrate.Node slices for DAG execution.
func (o *OrchestratorAgent) buildNodes(plan *Plan, req *schema.RunRequest, contextSummary string) ([]orchestrate.Node, error) {
	nodes := make([]orchestrate.Node, 0, len(plan.Steps)+1)

	for _, step := range plan.Steps {
		stepCopy := step
		var runner agent.Agent

		if stepCopy.DynamicSpec != nil {
			// Build dynamic agent from spec.
			dynAgent, err := o.buildDynamicAgent(stepCopy.ID, stepCopy.DynamicSpec)
			if err != nil {
				return nil, fmt.Errorf("orchestrator: build dynamic agent for step %q: %w", stepCopy.ID, err)
			}
			runner = dynAgent
		} else {
			// Existing static dispatch.
			subAgent, ok := o.subAgents[step.Agent]
			if !ok {
				subAgent, ok = o.subAgents["coder"]
				if !ok {
					return nil, fmt.Errorf("orchestrator: no agent available for %q", step.Agent)
				}
			}
			runner = subAgent
		}

		nodes = append(nodes, orchestrate.Node{
			ID:     step.ID,
			Runner: runner,
			Deps:   step.DependsOn,
			InputMapper: func(upstream map[string]*schema.RunResponse) (*schema.RunRequest, error) {
				var msgs []schema.Message
				if o.workingDir != "" {
					msgs = append(msgs, schema.NewUserMessage(
						fmt.Sprintf("Working directory: %s", o.workingDir),
					))
				}
				if contextSummary != "" {
					msgs = append(msgs, schema.NewUserMessage(
						fmt.Sprintf("Project context:\n%s", contextSummary),
					))
				}
				msgs = append(msgs, schema.NewUserMessage(
					fmt.Sprintf("Original request: %s", plan.Goal),
				))
				for _, depID := range stepCopy.DependsOn {
					if resp, ok := upstream[depID]; ok && resp != nil {
						msgs = append(msgs, resp.Messages...)
					}
				}
				msgs = append(msgs, schema.NewUserMessage(stepCopy.Description))
				return &schema.RunRequest{
					Messages:  msgs,
					SessionID: req.SessionID,
				}, nil
			},
			Optional: true,
		})
	}

	// Add summary node if there are multiple terminal nodes.
	terminalIDs := findTerminalNodes(nodes)
	if len(terminalIDs) > 1 {
		summaryNode := orchestrate.Node{
			ID:     "summary",
			Runner: o.planGen,
			Deps:   terminalIDs,
			InputMapper: func(upstream map[string]*schema.RunResponse) (*schema.RunRequest, error) {
				var sb strings.Builder
				sb.WriteString("Summarize the following completed step results:\n\n")
				for id, resp := range upstream {
					if resp != nil {
						fmt.Fprintf(&sb, "## Step: %s\n", id)
						for _, m := range resp.Messages {
							sb.WriteString(m.Content.Text())
							sb.WriteString("\n")
						}
						sb.WriteString("\n")
					}
				}
				return &schema.RunRequest{
					Messages:  []schema.Message{schema.NewUserMessage(sb.String())},
					SessionID: req.SessionID,
				}, nil
			},
		}
		nodes = append(nodes, summaryNode)
	}

	return nodes, nil
}

// findTerminalNodes returns IDs of nodes that have no downstream dependents.
func findTerminalNodes(nodes []orchestrate.Node) []string {
	hasDependents := make(map[string]bool)
	for _, n := range nodes {
		for _, dep := range n.Deps {
			hasDependents[dep] = true
		}
	}

	var terminals []string
	for _, n := range nodes {
		if !hasDependents[n.ID] {
			terminals = append(terminals, n.ID)
		}
	}

	return terminals
}

// PlanAggregator synthesizes sub-task outputs into a coherent response.
type PlanAggregator struct {
	summarizer *taskagent.Agent
}

// Aggregate combines step results into a summary response.
func (a *PlanAggregator) Aggregate(ctx context.Context, results map[string]*schema.RunResponse) (*schema.RunResponse, error) {
	if len(results) == 0 {
		return &schema.RunResponse{}, nil
	}

	if len(results) == 1 {
		for _, resp := range results {
			return resp, nil
		}
	}

	var sb strings.Builder
	sb.WriteString(PlanSummaryPrompt)
	sb.WriteString("\n\n")

	keys := make([]string, 0, len(results))
	for k := range results {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		resp := results[k]
		if resp == nil {
			fmt.Fprintf(&sb, "## %s\n(No output -- step may have been skipped or failed)\n\n", k)
			continue
		}
		fmt.Fprintf(&sb, "## %s\n", k)
		for _, m := range resp.Messages {
			sb.WriteString(m.Content.Text())
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	summaryReq := &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage(sb.String())},
	}

	return a.summarizer.Run(ctx, summaryReq)
}
