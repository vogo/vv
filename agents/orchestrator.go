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

// OrchestratorSystemPrompt is the system prompt for the orchestrator agent's classification call.
const OrchestratorSystemPrompt = `You are an orchestrator agent. You receive user instructions and decide how to fulfill them.

## Working Directory
The user is working in: {{.WorkingDir}}

## Available Agents
- "coder": Reads, writes, edits files, runs commands, searches codebases, debugs
- "researcher": Explores codebases, reads documentation, gathers information (read-only)
- "reviewer": Reviews code for correctness, style, performance, security
- "chat": General conversation, questions, explanations, brainstorming

## Response Format
You MUST respond with ONLY a JSON object. No other text.

### For simple tasks (single agent):
{"mode": "direct", "agent": "<agent_id>"}

### For complex tasks (multi-step):
{"mode": "plan", "plan": {"goal": "...", "steps": [{"id": "step_1", "description": "...", "agent": "coder", "depends_on": []}]}}

## Rules
1. Use "direct" mode for tasks that clearly map to one agent capability.
2. Use "plan" mode only when the task genuinely requires multiple distinct steps across different capabilities.
3. For plan steps, use "depends_on" to specify ordering. Steps without dependencies run in parallel.
4. Keep plans focused: typically 2-5 steps.
5. Default to "coder" for ambiguous coding tasks, "chat" for general questions.
`

const PlanSummaryPrompt = `You are summarizing the results of a multi-step task execution. Synthesize the outputs from all completed steps into a coherent, concise response for the user.

For each step result provided, note:
- What was accomplished
- Any errors or issues encountered
- Key outputs or artifacts produced

Provide a unified summary that directly addresses the original user request.`

// OrchestratorAgent is the main agent that receives all user requests.
// It decides whether to handle directly (single agent dispatch) or
// decompose into a multi-step plan executed as a DAG.
type OrchestratorAgent struct {
	agent.Base
	llm            aimodel.ChatCompleter
	model          string
	subAgents      map[string]agent.Agent // coder, researcher, reviewer, chat
	planGen        *taskagent.Agent       // LLM agent for plan summarization
	maxConcurrency int
	fallbackAgent  agent.Agent // chat agent as fallback
	workingDir     string      // captured CWD
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

// Run executes the orchestrator: classifies the request and dispatches accordingly.
func (o *OrchestratorAgent) Run(ctx context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	result, classifyUsage, err := o.classifyTask(ctx, req)
	if err != nil {
		slog.Warn("orchestrator: classification failed, falling back to chat", "error", err)
		return o.fallbackRun(ctx, req, classifyUsage)
	}

	switch result.Mode {
	case "direct":
		return o.runDirect(ctx, req, result, classifyUsage)
	case "plan":
		return o.runPlan(ctx, req, result.Plan, classifyUsage)
	default:
		slog.Warn("orchestrator: unknown mode, falling back to chat", "mode", result.Mode)
		return o.fallbackRun(ctx, req, classifyUsage)
	}
}

// RunStream implements streaming dispatch with two paths:
// direct mode proxies the sub-agent stream; plan mode wraps synchronous DAG execution.
func (o *OrchestratorAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	result, classifyUsage, err := o.classifyTask(ctx, req)
	if err != nil {
		slog.Warn("orchestrator: classification failed, falling back to chat stream", "error", err)
		return o.fallbackRunStream(ctx, req)
	}

	switch result.Mode {
	case "direct":
		subAgent, ok := o.subAgents[result.Agent]
		if !ok {
			return o.fallbackRunStream(ctx, req)
		}
		sa, ok := subAgent.(agent.StreamAgent)
		if !ok {
			return agent.RunToStream(ctx, subAgent, o.enrichRequest(req)), nil
		}
		return sa.RunStream(ctx, o.enrichRequest(req))
	case "plan":
		// Wrap plan execution as a stream without re-classifying. We create
		// a lightweight agent.Agent that runs the already-parsed plan directly.
		planRunner := agent.NewCustomAgent(
			agent.Config{ID: o.ID(), Name: o.Name(), Description: o.Description()},
			func(ctx context.Context, innerReq *schema.RunRequest) (*schema.RunResponse, error) {
				return o.runPlan(ctx, innerReq, result.Plan, classifyUsage)
			},
		)
		return agent.RunToStream(ctx, planRunner, req), nil
	default:
		return o.fallbackRunStream(ctx, req)
	}
}

// classifyTask makes a single LLM call to classify the user's request.
func (o *OrchestratorAgent) classifyTask(ctx context.Context, req *schema.RunRequest) (*ClassifyResult, *aimodel.Usage, error) {
	systemPrompt := strings.Replace(OrchestratorSystemPrompt, "{{.WorkingDir}}", o.workingDir, 1)

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

	// Extract text from the response.
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

// enrichRequest prepends working directory context to a request for sub-agent dispatch.
func (o *OrchestratorAgent) enrichRequest(req *schema.RunRequest) *schema.RunRequest {
	if o.workingDir == "" {
		return req
	}
	msgs := make([]schema.Message, 0, len(req.Messages)+1)
	msgs = append(msgs, schema.NewUserMessage(
		fmt.Sprintf("Working directory: %s", o.workingDir),
	))
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

	resp, err := subAgent.Run(ctx, o.enrichRequest(req))
	if err != nil {
		return nil, fmt.Errorf("orchestrator: sub-agent %q failed: %w", cr.Agent, err)
	}

	resp.Usage = aggregateUsage(classifyUsage, resp.Usage)
	return resp, nil
}

// runPlan builds and executes a DAG from the plan.
func (o *OrchestratorAgent) runPlan(ctx context.Context, req *schema.RunRequest, plan *Plan, classifyUsage *aimodel.Usage) (*schema.RunResponse, error) {
	nodes, err := o.buildNodes(plan, req)
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

// aggregateUsage merges classify and sub-agent usage into a single Usage.
func aggregateUsage(classify *aimodel.Usage, sub *aimodel.Usage) *aimodel.Usage {
	if classify == nil && sub == nil {
		return nil
	}
	result := &aimodel.Usage{}
	if classify != nil {
		result.Add(classify)
	}
	if sub != nil {
		result.Add(sub)
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
func (o *OrchestratorAgent) buildNodes(plan *Plan, req *schema.RunRequest) ([]orchestrate.Node, error) {
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
				// Add working directory context.
				if o.workingDir != "" {
					msgs = append(msgs, schema.NewUserMessage(
						fmt.Sprintf("Working directory: %s", o.workingDir),
					))
				}
				// Add original user goal for context.
				msgs = append(msgs, schema.NewUserMessage(
					fmt.Sprintf("Original request: %s", plan.Goal),
				))
				// Prepend upstream context if available.
				for _, depID := range stepCopy.DependsOn {
					if resp, ok := upstream[depID]; ok && resp != nil {
						msgs = append(msgs, resp.Messages...)
					}
				}
				// Add the step description as the task.
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

	// If only one result, return it directly.
	if len(results) == 1 {
		for _, resp := range results {
			return resp, nil
		}
	}

	// Build summary prompt from all step results.
	var sb strings.Builder
	sb.WriteString(PlanSummaryPrompt)
	sb.WriteString("\n\n")

	// Sort keys for deterministic ordering.
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
