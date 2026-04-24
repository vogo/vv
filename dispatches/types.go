package dispatches

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/registries"
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
func (cr *ClassifyResult) validate(reg *registries.Registry, subAgents map[string]agent.Agent) error {
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

// IntentType classifies the complexity of a user request.
type IntentType string

const (
	IntentSimple  IntentType = "simple"  // single agent, no planning
	IntentComplex IntentType = "complex" // multi-step, needs planning
)

// IntentResult is the output of the intent recognition phase.
// Mode uses string values "direct" (maps to IntentSimple), "plan" (maps to IntentComplex),
// and "answered" (unified-intent answered inline; Answer carries the response text).
// JSON compatibility is preserved for the two legacy values.
type IntentResult struct {
	NeedsExploration bool   `json:"needs_exploration"`
	Mode             string `json:"mode"`             // "direct" | "plan" | "answered"
	Agent            string `json:"agent,omitempty"`  // for direct mode
	Plan             *Plan  `json:"plan,omitempty"`   // for plan mode
	Answer           string `json:"answer,omitempty"` // for answered mode (unified intent)
}

// IntentModeAnswered is the Mode value produced by unified intent when the
// model chooses answer_directly: Dispatcher.Run returns the Answer text
// without invoking a sub-agent.
const IntentModeAnswered = "answered"

// validate checks that the intent result references valid agents.
func (ir *IntentResult) validate(reg *registries.Registry, subAgents map[string]agent.Agent) error {
	switch ir.Mode {
	case "direct":
		if _, ok := subAgents[ir.Agent]; !ok {
			return fmt.Errorf("unknown agent %q in direct dispatch", ir.Agent)
		}
	case "plan":
		if ir.Plan == nil || len(ir.Plan.Steps) == 0 {
			return fmt.Errorf("plan mode but no steps provided")
		}

		for _, step := range ir.Plan.Steps {
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
	case IntentModeAnswered:
		if strings.TrimSpace(ir.Answer) == "" {
			return fmt.Errorf("answered mode but empty answer")
		}
	default:
		return fmt.Errorf("unknown intent mode %q", ir.Mode)
	}

	return nil
}

// ReplanPolicy controls dynamic replanning behavior.
type ReplanPolicy struct {
	TriggerOnFailure   bool `yaml:"trigger_on_failure"`
	TriggerOnDeviation bool `yaml:"trigger_on_deviation"` // reserved for future use; not implemented
	MaxReplans         int  `yaml:"max_replans"`
}

// DefaultReplanPolicy returns a ReplanPolicy with sensible defaults.
// All triggers are disabled for backward compatibility.
func DefaultReplanPolicy() ReplanPolicy {
	return ReplanPolicy{
		TriggerOnFailure:   false,
		TriggerOnDeviation: false,
		MaxReplans:         2,
	}
}

// SummaryPolicy controls when summarization occurs.
type SummaryPolicy string

const (
	SummaryAuto   SummaryPolicy = "auto" // HTTP=yes, CLI=no
	SummaryAlways SummaryPolicy = "always"
	SummaryNever  SummaryPolicy = "never"
)

// Plan represents a parsed execution plan.
type Plan struct {
	Goal        string     `json:"goal"`
	Steps       []PlanStep `json:"steps"`
	ReplanCount int        `json:"replan_count,omitempty"` // how many times replanned
	MaxReplans  int        `json:"max_replans,omitempty"`  // from ReplanPolicy
}

// PlanStep represents a single step in the plan.
type PlanStep struct {
	ID               string            `json:"id"`
	Description      string            `json:"description"`
	Agent            string            `json:"agent"`
	DependsOn        []string          `json:"depends_on"`
	DynamicSpec      *DynamicAgentSpec `json:"dynamic_spec,omitempty"`
	ReplanGeneration int               `json:"replan_generation,omitempty"` // 0=original, 1=first replan, etc.
}

// DynamicAgentSpec defines configuration for a dynamically created sub-agent.
type DynamicAgentSpec struct {
	BaseType     string `json:"base_type"`               // required: coder, researcher, reviewer, chat
	SystemPrompt string `json:"system_prompt,omitempty"` // optional: custom system prompt
	ToolAccess   string `json:"tool_access,omitempty"`   // optional: profile name string for JSON compat
	Model        string `json:"model,omitempty"`         // optional: overrides configured model
}

// validate checks that a DynamicAgentSpec is well-formed.
func (s *DynamicAgentSpec) validate(reg *registries.Registry) error {
	if s.BaseType == "" {
		return fmt.Errorf("dynamic_spec: base_type is required")
	}

	if !reg.ValidateRef(s.BaseType) {
		return fmt.Errorf("dynamic_spec: invalid base_type %q", s.BaseType)
	}

	if s.ToolAccess != "" {
		if _, ok := registries.ProfileByName(s.ToolAccess); !ok {
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
