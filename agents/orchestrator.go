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
	"github.com/vogo/vage/schema"
)

const PlanSummaryPrompt = `You are summarizing the results of a multi-step task execution. Synthesize the outputs from all completed steps into a coherent, concise response for the user.

For each step result provided, note:
- What was accomplished
- Any errors or issues encountered
- Key outputs or artifacts produced

Provide a unified summary that directly addresses the original user request.`

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

	explorerAgent agent.Agent // explores codebase to build context (nil if not configured)
	plannerAgent  agent.Agent // classifies/plans tasks
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
			if _, ok := subAgents[step.Agent]; !ok {
				return fmt.Errorf("unknown agent %q in plan step %q", step.Agent, step.ID)
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
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Agent       string   `json:"agent"`
	DependsOn   []string `json:"depends_on"`
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

// buildNodes converts a Plan into orchestrate.Node slices for DAG execution.
func (o *OrchestratorAgent) buildNodes(plan *Plan, req *schema.RunRequest, contextSummary string) ([]orchestrate.Node, error) {
	nodes := make([]orchestrate.Node, 0, len(plan.Steps)+1)

	for _, step := range plan.Steps {
		stepCopy := step
		subAgent, ok := o.subAgents[step.Agent]
		if !ok {
			subAgent, ok = o.subAgents["coder"]
			if !ok {
				return nil, fmt.Errorf("orchestrator: no agent available for %q", step.Agent)
			}
		}

		nodes = append(nodes, orchestrate.Node{
			ID:     step.ID,
			Runner: subAgent,
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
