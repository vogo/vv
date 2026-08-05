package dispatches

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/sessionlogs"
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

// PlanTaskToolDescription is the description advertised to the LLM for the
// `plan_task` tool. It states the same four-condition gate as the Primary
// system prompt (agents.PrimarySystemPrompt): DAG planning is an advanced,
// opt-in path, not the default route for ordinary multi-step coding work.
//
// Exported so drift between the prompt contract and the tool contract can be
// asserted in tests — a tool description that keeps advertising "spans
// multiple capabilities" would silently re-open the over-planning behaviour
// the prompt is trying to close.
const PlanTaskToolDescription = "Advanced parallel orchestration — NOT the default path for multi-step work. " +
	"Run a DAG only when ALL FOUR conditions hold: (a) the work splits into at least two genuinely independent workflows, " +
	"not consecutive slices of one change; (b) running them in parallel saves real wall-clock time; " +
	"(c) their ordering is clear enough to express with depends_on, or the branches have no dependencies at all; " +
	"(d) the user asked for parallel execution, or asked for a long task to run in the background. " +
	"An ordinary bug fix, a single-file or single-symbol change, and any task that only needs a sequential checklist " +
	"must be done directly instead — use todo_write for progress. " +
	"Typical fits: repo-wide migrations, independent modules implemented side by side, research + implementation + review in parallel. " +
	"Each step names an agent and lists dependencies; returns the synthesised result of the DAG once all steps complete."

// PlanExecutor abstracts the dispatcher's multi-step plan execution so the
// `plan_task` tool can drive a DAG without holding a *Dispatcher (which would
// re-export internal state). The dispatcher implements this interface via
// (*Dispatcher).RunPlan.
type PlanExecutor interface {
	RunPlan(ctx context.Context, plan *Plan, req *schema.RunRequest) (*schema.RunResponse, error)
	Protocol() schema.Protocol
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

		// The specialist runs under the caller's session so its work is
		// persisted at all: without a session id every checkpoint Save
		// fails with ErrInvalidArgument and every event it emits is
		// dropped by SessionHook, which is why delegated work used to
		// leave nothing on disk. vage puts the id on the tool handler's
		// ctx (taskagent tool_batch), so it is simply read back here.
		sessionID := schema.SessionIDFromContext(ctx)

		req := schema.RunRequest{
			Messages:  []schema.Message{schema.NewUserMessage(ag.Protocol(), input)},
			SessionID: sessionID,
		}

		// Tag the dispatch so the transcript store files it under
		// subagents/<agent>-<n>.jsonl instead of appending to the
		// session's own resume timeline.
		ctx = sessionlogs.WithRun(ctx, sessionlogs.Run{
			Key:  sessionlogs.NewRunKey(),
			Task: task,
		})

		// In a streaming Primary run taskagent exposes the active stream through
		// the context emitter.  Consume the specialist's stream here instead of
		// hiding it behind Agent.Run, so its tool calls and progress remain
		// visible to CLI/SSE consumers.  Sync callers have no emitter and retain
		// the original non-streaming path.
		if emitter := schema.EmitterFromContext(ctx); emitter != nil {
			text, err := runDelegateStream(ctx, emitter, ag, &req, task)
			if err != nil {
				return schema.ErrorResult("", "delegate tool: execution failed: "+err.Error()), nil
			}

			return schema.TextResult("", text), nil
		}

		resp, err := ag.Run(ctx, &req)
		if err != nil {
			return schema.ErrorResult("", "delegate tool: execution failed: "+err.Error()), nil
		}

		var parts []string
		for _, msg := range resp.Messages {
			if msg.Role() == schema.RoleAssistant {
				if text := msg.Text(); text != "" {
					parts = append(parts, text)
				}
			}
		}

		return schema.TextResult("", strings.Join(parts, "\n")), nil
	}
}

// runDelegateStream relays a delegated agent into the active parent stream
// while retaining its final answer as the delegate tool result consumed by
// Primary. SubAgentStart/End form the UI nesting boundary; the child's native
// events between them are forwarded unchanged.
func runDelegateStream(
	ctx context.Context,
	emit schema.Emitter,
	ag agent.Agent,
	req *schema.RunRequest,
	task string,
) (string, error) {
	if err := emit(schema.NewEvent(schema.EventSubAgentStart, ag.ID(), req.SessionID, schema.SubAgentStartData{
		AgentName:   ag.ID(),
		Description: task,
	})); err != nil {
		return "", err
	}

	started := time.Now()
	toolCalls := 0
	promptTokens := 0
	completionTokens := 0
	finalText := ""
	var streamedText strings.Builder
	emitEnd := func() error {
		return emit(schema.NewEvent(schema.EventSubAgentEnd, ag.ID(), req.SessionID, schema.SubAgentEndData{
			AgentName:        ag.ID(),
			Duration:         time.Since(started).Milliseconds(),
			ToolCalls:        toolCalls,
			TokensUsed:       promptTokens + completionTokens,
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
		}))
	}

	streamAgent, ok := ag.(agent.StreamAgent)
	if !ok {
		resp, err := ag.Run(ctx, req)
		if err != nil {
			_ = emitEnd()
			return "", err
		}

		finalText = assistantResponseText(resp)
		if resp != nil && resp.Usage != nil {
			promptTokens = resp.Usage.PromptTokens
			completionTokens = resp.Usage.CompletionTokens
		}
		if finalText != "" {
			if err := emit(schema.NewEvent(schema.EventTextDelta, ag.ID(), req.SessionID, schema.TextDeltaData{Delta: finalText})); err != nil {
				return "", err
			}
		}
		if err := emitEnd(); err != nil {
			return "", err
		}

		return finalText, nil
	}

	stream, err := streamAgent.RunStream(ctx, req)
	if err != nil {
		_ = emitEnd()
		return "", err
	}
	defer func() { _ = stream.Close() }()

	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}

			_ = emitEnd()
			return "", recvErr
		}

		switch event.Type {
		case schema.EventTextDelta:
			if data, ok := event.Data.(schema.TextDeltaData); ok {
				streamedText.WriteString(data.Delta)
			}
		case schema.EventToolCallStart:
			toolCalls++
		case schema.EventLLMCallEnd:
			if data, ok := event.Data.(schema.LLMCallEndData); ok {
				promptTokens += data.PromptTokens
				completionTokens += data.CompletionTokens
			}
		case schema.EventAgentEnd:
			if data, ok := event.Data.(schema.AgentEndData); ok && data.Message != "" {
				finalText = data.Message
			}
		}

		if err := emit(event); err != nil {
			return "", err
		}
	}

	if finalText == "" {
		finalText = streamedText.String()
	}

	if err := emitEnd(); err != nil {
		return "", err
	}

	return finalText, nil
}

func assistantResponseText(resp *schema.RunResponse) string {
	if resp == nil {
		return ""
	}

	var parts []string
	for _, msg := range resp.Messages {
		if msg.Role() == schema.RoleAssistant {
			if text := msg.Text(); text != "" {
				parts = append(parts, text)
			}
		}
	}

	return strings.Join(parts, "\n")
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
				"type": "array",
				"description": "One step per genuinely independent workflow; keep the plan to 2-5 steps. " +
					"Use depends_on for ordering; steps with no dependencies run in parallel. " +
					"Do not slice a single sequential change into artificial steps: if the steps would just be consecutive parts of the same edit, " +
					"skip this tool and do the work directly, tracking progress with todo_write.",
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
		Description: PlanTaskToolDescription,
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
			Messages: []schema.Message{schema.NewUserMessage(exec.Protocol(), parsed.Goal)},
		}

		resp, err := exec.RunPlan(ctx, plan, req)
		if err != nil {
			return schema.ErrorResult("", "plan_task: execution failed: "+err.Error()), nil
		}

		var parts []string
		if resp != nil {
			for _, msg := range resp.Messages {
				if text := msg.Text(); text != "" {
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
