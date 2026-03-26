package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/orchestrate"
	"github.com/vogo/vage/schema"
)

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

// PlannerAgent is a two-phase agent: plan generation + DAG execution.
// It implements both agent.Agent and agent.StreamAgent.
type PlannerAgent struct {
	agent.Base
	planGen        *taskagent.Agent       // LLM for plan generation
	subAgents      map[string]agent.Agent // available delegation targets
	maxConcurrency int                    // configurable, default from config
	fallbackAgent  agent.Agent            // fallback when plan generation fails
}

// Compile-time checks.
var (
	_ agent.Agent       = (*PlannerAgent)(nil)
	_ agent.StreamAgent = (*PlannerAgent)(nil)
)

// NewPlannerAgent creates a new PlannerAgent.
func NewPlannerAgent(cfg agent.Config, planGen *taskagent.Agent, subAgents map[string]agent.Agent, maxConcurrency int, fallback agent.Agent) *PlannerAgent {
	return &PlannerAgent{
		Base:           agent.NewBase(cfg),
		planGen:        planGen,
		subAgents:      subAgents,
		maxConcurrency: maxConcurrency,
		fallbackAgent:  fallback,
	}
}

// Run executes the planner: generates a plan, then executes it as a DAG.
func (p *PlannerAgent) Run(ctx context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	// Phase 1: Generate plan.
	planReq := &schema.RunRequest{
		Messages:  req.Messages,
		SessionID: req.SessionID,
	}

	planResp, err := p.planGen.Run(ctx, planReq)
	if err != nil {
		slog.Warn("planner: plan generation failed, falling back to coder", "error", err)
		return p.fallbackRun(ctx, req)
	}

	plan, err := parsePlan(planResp)
	if err != nil {
		slog.Warn("planner: plan parsing failed, falling back to coder", "error", err)
		return p.fallbackRun(ctx, req)
	}

	if len(plan.Steps) == 0 {
		slog.Warn("planner: empty plan, falling back to coder")
		return p.fallbackRun(ctx, req)
	}

	// Phase 2: Build and execute DAG.
	nodes, err := p.buildNodes(plan, req)
	if err != nil {
		slog.Warn("planner: DAG build failed, falling back to coder", "error", err)
		return p.fallbackRun(ctx, req)
	}

	dagCfg := orchestrate.DAGConfig{
		MaxConcurrency: p.maxConcurrency,
		ErrorStrategy:  orchestrate.Skip,
		Aggregator:     &PlanAggregator{summarizer: p.planGen},
	}

	result, err := orchestrate.ExecuteDAG(ctx, dagCfg, nodes, req)
	if err != nil {
		return nil, fmt.Errorf("planner: DAG execution failed: %w", err)
	}

	return result.FinalOutput, nil
}

// RunStream wraps Run with lifecycle events for streaming compatibility.
func (p *PlannerAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return agent.RunToStream(ctx, p, req), nil
}

// fallbackRun delegates to the fallback agent with a warning prepended.
func (p *PlannerAgent) fallbackRun(ctx context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	if p.fallbackAgent == nil {
		return nil, fmt.Errorf("planner: no fallback agent available")
	}

	// Prepend context about the fallback.
	msgs := make([]schema.Message, 0, len(req.Messages)+1)
	msgs = append(msgs, schema.NewUserMessage("Note: task planning failed, executing as a single task."))
	msgs = append(msgs, req.Messages...)

	fallbackReq := &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
		Options:   req.Options,
		Metadata:  req.Metadata,
	}

	return p.fallbackAgent.Run(ctx, fallbackReq)
}

// parsePlan extracts a Plan from the LLM response.
func parsePlan(resp *schema.RunResponse) (*Plan, error) {
	if resp == nil || len(resp.Messages) == 0 {
		return nil, fmt.Errorf("empty plan response")
	}

	text := resp.Messages[0].Content.Text()
	if text == "" {
		return nil, fmt.Errorf("empty plan text")
	}

	// Try to extract JSON from the response (may have markdown fences).
	jsonStr := extractJSON(text)

	var plan Plan
	if err := json.Unmarshal([]byte(jsonStr), &plan); err != nil {
		return nil, fmt.Errorf("parse plan JSON: %w", err)
	}

	// Validate plan steps reference valid agents.
	validAgents := map[string]bool{"coder": true, "researcher": true, "reviewer": true}
	for _, step := range plan.Steps {
		if !validAgents[step.Agent] {
			return nil, fmt.Errorf("invalid agent %q in step %q", step.Agent, step.ID)
		}
	}

	return &plan, nil
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

	// Try to find a JSON object directly.
	if start := strings.Index(text, "{"); start >= 0 {
		// Find the matching closing brace.
		depth := 0
		for i := start; i < len(text); i++ {
			switch text[i] {
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
func (p *PlannerAgent) buildNodes(plan *Plan, req *schema.RunRequest) ([]orchestrate.Node, error) {
	nodes := make([]orchestrate.Node, 0, len(plan.Steps)+1) // +1 for potential summary node

	for _, step := range plan.Steps {
		stepCopy := step // capture for closure
		subAgent, ok := p.subAgents[step.Agent]
		if !ok {
			// Fall back to coder if agent not found.
			subAgent, ok = p.subAgents["coder"]
			if !ok {
				return nil, fmt.Errorf("planner: no agent available for %q", step.Agent)
			}
		}

		nodes = append(nodes, orchestrate.Node{
			ID:     step.ID,
			Runner: subAgent,
			Deps:   step.DependsOn,
			InputMapper: func(upstream map[string]*schema.RunResponse) (*schema.RunRequest, error) {
				var msgs []schema.Message
				// Prepend upstream context if available.
				for _, depID := range stepCopy.DependsOn {
					if resp, ok := upstream[depID]; ok && resp != nil {
						msgs = append(msgs, resp.Messages...)
					}
				}
				// Add the step description as the user message.
				msgs = append(msgs, schema.NewUserMessage(stepCopy.Description))
				return &schema.RunRequest{
					Messages:  msgs,
					SessionID: req.SessionID,
				}, nil
			},
			Optional: true, // Allow DAG to continue if a step fails.
		})
	}

	// Add summary node if there are multiple terminal nodes.
	terminalIDs := findTerminalNodes(nodes)
	if len(terminalIDs) > 1 {
		summaryNode := orchestrate.Node{
			ID:     "summary",
			Runner: p.planGen,
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
