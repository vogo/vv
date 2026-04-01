package dispatch

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/registry"
)

// PlanSummaryPrompt is the system prompt for plan summarization.
const PlanSummaryPrompt = `You are summarizing the results of a multi-step task execution. Synthesize the outputs from all completed steps into a coherent, concise response for the user.

For each step result provided, note:
- What was accomplished
- Any errors or issues encountered
- Key outputs or artifacts produced

Provide a unified summary that directly addresses the original user request.`

// ClassifyResult is the structured LLM response for task classification.
type ClassifyResult struct {
	Mode  string `json:"mode"`  // "direct" or "plan"
	Agent string `json:"agent"` // only for mode="direct": coder/researcher/reviewer/chat
	Plan  *Plan  `json:"plan"`  // only for mode="plan"
}

// validate checks that the classification result references valid agents.
// Uses the registry instead of a map[string]agent.Agent.
func (cr *ClassifyResult) validate(reg *registry.Registry, subAgents map[string]agent.Agent) error {
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
				if err := step.DynamicSpec.validate(reg); err != nil {
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

// Plan represents a parsed execution plan.
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
	DynamicSpec *DynamicAgentSpec `json:"dynamic_spec,omitempty"`
}

// DynamicAgentSpec defines configuration for a dynamically created sub-agent.
type DynamicAgentSpec struct {
	BaseType     string `json:"base_type"`               // required: coder, researcher, reviewer, chat
	SystemPrompt string `json:"system_prompt,omitempty"` // optional: custom system prompt
	ToolAccess   string `json:"tool_access,omitempty"`   // optional: profile name string for JSON compat
	Model        string `json:"model,omitempty"`         // optional: overrides configured model
}

// validate checks that a DynamicAgentSpec is well-formed.
func (s *DynamicAgentSpec) validate(reg *registry.Registry) error {
	if s.BaseType == "" {
		return fmt.Errorf("dynamic_spec: base_type is required")
	}

	if !reg.ValidateRef(s.BaseType) {
		return fmt.Errorf("dynamic_spec: invalid base_type %q", s.BaseType)
	}

	if s.ToolAccess != "" {
		if _, ok := registry.ProfileByName(s.ToolAccess); !ok {
			return fmt.Errorf("dynamic_spec: invalid tool_access %q", s.ToolAccess)
		}
	}

	return nil
}

// PlanAggregator synthesizes sub-task outputs into a coherent response.
type PlanAggregator struct {
	Summarizer agent.Agent // uses agent.Agent interface, not *taskagent.Agent
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

	return a.Summarizer.Run(ctx, summaryReq)
}
