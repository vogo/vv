package dispatches

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
)

// Primary Assistant tool name constants. Exported so tests and observability
// can refer to them by symbol rather than string literal.
const (
	PrimaryToolPlanTask = "plan_task"

	// PrimaryDelegateToolPrefix is the name prefix shared by every per-agent
	// delegation tool registered for the Primary Assistant. Tools are named
	// `delegate_to_<agentID>` (e.g. `delegate_to_coder`).
	PrimaryDelegateToolPrefix = "delegate_to_"
)

// PlanExecutor abstracts the dispatcher's multi-step plan execution so the
// `plan_task` tool can drive a DAG without holding a *Dispatcher (which would
// re-export internal state). The dispatcher implements this interface via
// (*Dispatcher).RunPlan.
type PlanExecutor interface {
	RunPlan(ctx context.Context, plan *Plan, req *schema.RunRequest) (*schema.RunResponse, error)
}

// DelegateToolName returns the tool name used for delegating to the given
// sub-agent (e.g. "coder" → "delegate_to_coder"). Centralised here so
// dispatcher and Primary system prompt builders can stay in sync.
func DelegateToolName(agentID string) string {
	return PrimaryDelegateToolPrefix + agentID
}

// delegateArgs is the parsed argument schema for delegate_to_<agent> tool calls.
// `task` carries the imperative instruction; `context` carries any additional
// background the Primary already gathered (file paths, prior findings, etc.)
// and is concatenated into the user message sent to the specialist.
type delegateArgs struct {
	Task    string `json:"task"`
	Context string `json:"context,omitempty"`
}

// delegateParameters returns the JSON Schema advertised to the LLM for any
// `delegate_to_<agent>` tool. Identical for every specialist; the agent
// identity is encoded in the tool name itself.
func delegateParameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "The imperative instruction for the specialist sub-agent. Be concrete and self-contained.",
			},
			"context": map[string]any{
				"type":        "string",
				"description": "Optional background already gathered (file paths, prior findings) the specialist should know.",
			},
		},
		"required": []string{"task"},
	}
}

// RegisterDelegateTools installs one `delegate_to_<id>` tool per id in `ids`
// onto reg. Each tool's handler increments the recursion depth on the context
// before invoking the underlying agent so a Primary → coder → re-Primary loop
// is bounded by Dispatcher.maxRecursionDepth via the same DepthFrom check
// the dispatcher uses for its own recursion guard.
//
// Unknown ids are skipped silently; the caller controls the slice. The
// existing per-agent tool name is reserved with RegisterIfAbsent so a typo
// or duplicate registration surfaces at startup.
func RegisterDelegateTools(reg tool.ToolRegistry, subAgents map[string]agent.Agent, ids []string) error {
	for _, id := range ids {
		ag, ok := subAgents[id]
		if !ok {
			continue
		}

		def := schema.ToolDef{
			Name:        DelegateToolName(id),
			Description: fmt.Sprintf("Delegate the current request to the %q sub-agent. Use this for work that cleanly maps to that specialist's capabilities.", id),
			Parameters:  delegateParameters(),
			Source:      schema.ToolSourceAgent,
			AgentID:     ag.ID(),
		}

		handler := newDelegateHandler(ag)

		if err := registerIfAbsent(reg, def, handler); err != nil {
			return fmt.Errorf("register delegate tool %q: %w", def.Name, err)
		}
	}

	return nil
}

// newDelegateHandler builds the closure that runs a sub-agent with the
// Primary-supplied task/context arguments. Errors are surfaced as ToolResult
// with IsError=true so the Primary LLM can read and react instead of the
// dispatcher aborting.
func newDelegateHandler(ag agent.Agent) tool.ToolHandler {
	return func(ctx context.Context, _ string, args string) (schema.ToolResult, error) {
		var parsed delegateArgs
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			return schema.ErrorResult("", "delegate tool: invalid arguments: "+err.Error()), nil
		}

		task := strings.TrimSpace(parsed.Task)
		if task == "" {
			return schema.ErrorResult("", "delegate tool: 'task' must be a non-empty string"), nil
		}

		// Increment recursion depth so the specialist (and any nested
		// dispatcher invocation it triggers) shares the Primary's budget.
		ctx = IncrementDepth(ctx)

		input := task
		if extra := strings.TrimSpace(parsed.Context); extra != "" {
			input = "Task: " + task + "\n\nContext:\n" + extra
		}

		req := schema.RunRequest{
			Messages: []schema.Message{schema.NewUserMessage(input)},
		}

		resp, err := ag.Run(ctx, &req)
		if err != nil {
			return schema.ErrorResult("", "delegate tool: execution failed: "+err.Error()), nil
		}

		var parts []string
		for _, msg := range resp.Messages {
			if msg.Role == aimodel.RoleAssistant {
				if text := msg.Content.Text(); text != "" {
					parts = append(parts, text)
				}
			}
		}

		return schema.TextResult("", strings.Join(parts, "\n")), nil
	}
}

// planTaskArgs mirrors the unified plan_task parameters so the LLM can pass
// identical structures whether it picks plan_task at the dispatcher gate or
// as a Primary tool.
type primaryPlanTaskArgs struct {
	Goal  string     `json:"goal"`
	Steps []PlanStep `json:"steps"`
}

// planTaskParameters returns the JSON Schema for the plan_task tool. Kept in
// lockstep with the dispatcher-side schema so prompt-cache friendly schemas
// can be reused across both call sites.
func planTaskParameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"goal": map[string]any{
				"type":        "string",
				"description": "The overall objective of the plan.",
			},
			"steps": map[string]any{
				"type":        "array",
				"description": "Ordered list of steps. Use depends_on for ordering; steps with no dependencies run in parallel.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":          map[string]any{"type": "string"},
						"description": map[string]any{"type": "string"},
						"agent":       map[string]any{"type": "string"},
						"depends_on": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "string"},
						},
					},
					"required": []string{"id", "description", "agent"},
				},
			},
		},
		"required": []string{"goal", "steps"},
	}
}

// RegisterPlanTaskTool installs the `plan_task` tool onto reg. Invoking the
// tool drives `exec.RunPlan` and returns the aggregated plan response text
// to the Primary so it can fold the result into its final assistant message.
//
// Validation reuses IntentResult-style checks via Plan.validate so the
// Primary receives a structured tool error (rather than an opaque failure)
// when it requests an unknown agent or an empty plan.
func RegisterPlanTaskTool(reg tool.ToolRegistry, exec PlanExecutor) error {
	if exec == nil {
		return fmt.Errorf("plan_task: executor is required")
	}

	def := schema.ToolDef{
		Name:        PrimaryToolPlanTask,
		Description: "Run a multi-step plan when the task spans multiple distinct sub-agent capabilities. Each step names an agent and lists dependencies. Returns the synthesised result of the DAG once all steps complete.",
		Parameters:  planTaskParameters(),
		Source:      schema.ToolSourceLocal,
	}

	handler := newPlanTaskHandler(exec)

	if err := registerIfAbsent(reg, def, handler); err != nil {
		return fmt.Errorf("register plan_task tool: %w", err)
	}

	return nil
}

// newPlanTaskHandler returns the ToolHandler closure that drives PlanExecutor.
func newPlanTaskHandler(exec PlanExecutor) tool.ToolHandler {
	return func(ctx context.Context, _ string, args string) (schema.ToolResult, error) {
		var parsed primaryPlanTaskArgs
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			return schema.ErrorResult("", "plan_task: invalid arguments: "+err.Error()), nil
		}

		if strings.TrimSpace(parsed.Goal) == "" {
			return schema.ErrorResult("", "plan_task: 'goal' must be a non-empty string"), nil
		}

		if len(parsed.Steps) == 0 {
			return schema.ErrorResult("", "plan_task: 'steps' must contain at least one step"), nil
		}

		plan := &Plan{Goal: parsed.Goal, Steps: parsed.Steps}

		// Primary's recursion budget covers the DAG too: increment so any
		// agent invoked by RunPlan inherits the next depth.
		ctx = IncrementDepth(ctx)

		req := &schema.RunRequest{
			Messages: []schema.Message{schema.NewUserMessage(parsed.Goal)},
		}

		resp, err := exec.RunPlan(ctx, plan, req)
		if err != nil {
			return schema.ErrorResult("", "plan_task: execution failed: "+err.Error()), nil
		}

		var parts []string
		if resp != nil {
			for _, msg := range resp.Messages {
				if text := msg.Content.Text(); text != "" {
					parts = append(parts, text)
				}
			}
		}

		return schema.TextResult("", strings.Join(parts, "\n")), nil
	}
}

// registerIfAbsent prefers RegisterIfAbsent on concrete *tool.Registry so we
// never silently overwrite a previously installed handler with the same name;
// for wrapped ToolRegistry implementations that lack the method, falls back
// to Register.
func registerIfAbsent(reg tool.ToolRegistry, def schema.ToolDef, handler tool.ToolHandler) error {
	type ifAbsent interface {
		RegisterIfAbsent(schema.ToolDef, tool.ToolHandler) error
	}

	if r, ok := reg.(ifAbsent); ok {
		return r.RegisterIfAbsent(def, handler)
	}

	return reg.Register(def, handler)
}
