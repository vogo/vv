package dispatches

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/debugs"
	"github.com/vogo/vv/registries"
)

// Unified-intent tool names. Kept exported so tests (and downstream
// observability code) can reference them by constant rather than string
// literal.
const (
	UnifiedToolAnswerDirectly = "answer_directly"
	UnifiedToolDelegateTo     = "delegate_to"
	UnifiedToolPlanTask       = "plan_task"

	// UnifiedIntentPhase is the Phase label for the single LLM call that
	// replaces the classic intent phase under unified mode.
	UnifiedIntentPhase = "unified_intent"

	// unifiedAnsweredSummary is emitted in PhaseEnd.Summary whenever the
	// unified-intent call chose answer_directly. Dashboards use it to count
	// the 1-LLM-call fast path that this milestone introduces. The classic
	// "Direct -> <agent>" and plan-goal summaries are preserved for the
	// other two paths so existing log/event consumers stay unchanged.
	unifiedAnsweredSummary = "unified_intent -> answered"
)

// unifiedIntentSystemPromptTemplate is the system prompt used when unified
// intent is enabled. It asks the model to either answer the user directly
// (folding the chat agent's responsibilities into this single call) or pick
// one of the delegation tools for tasks that genuinely need it.
//
// Placeholders: {{.AgentList}}, {{.WorkingDir}}, {{.ChatGuidelines}}.
const unifiedIntentSystemPromptTemplate = `You are the front door of a coding assistant. On every user message, you must do exactly one of three things by calling one of the provided tools:

1. ` + "`" + UnifiedToolAnswerDirectly + "`" + ` — Answer the user inline. Use this for greetings, general knowledge, small talk, simple math, definitions, and anything that does not require reading or modifying project files. This is the preferred default whenever possible.
2. ` + "`" + UnifiedToolDelegateTo + "`" + ` — Hand the request to a specialist sub-agent. Use this when the user clearly wants code changes, file reads, a review, or other sub-agent work.
3. ` + "`" + UnifiedToolPlanTask + "`" + ` — Produce a multi-step plan when the task genuinely spans multiple distinct capabilities. Keep plans focused (typically 2–5 steps).

## Available sub-agents
{{.AgentList}}

## Working directory
{{.WorkingDir}}

## When you answer directly
{{.ChatGuidelines}}

## Rules
- Always call exactly one tool. Do not return plain text instead of a tool call.
- Prefer ` + "`" + UnifiedToolAnswerDirectly + "`" + ` for anything that is a question, greeting, or general-knowledge lookup.
- Use ` + "`" + UnifiedToolDelegateTo + "`" + ` for clearly single-agent project work; default ` + "`" + `coder` + "`" + ` for ambiguous coding tasks.
- Use ` + "`" + UnifiedToolPlanTask + "`" + ` only when the task requires multiple steps across different capabilities.
- For plan steps, ` + "`" + `depends_on` + "`" + ` expresses ordering; steps with no dependencies run in parallel.`

// BuildUnifiedIntentSystemPrompt constructs the unified-intent prompt from
// the registry. It substitutes the agent list, working directory, and chat
// guidelines into the template.
func BuildUnifiedIntentSystemPrompt(reg *registries.Registry, workingDir, chatGuidelines string) string {
	p := unifiedIntentSystemPromptTemplate
	p = strings.Replace(p, "{{.AgentList}}", reg.PlannerAgentList(), 1)
	p = strings.Replace(p, "{{.WorkingDir}}", workingDir, 1)
	p = strings.Replace(p, "{{.ChatGuidelines}}", chatGuidelines, 1)
	return p
}

// unifiedIntentTools returns the three-tool schema advertised to the LLM.
// Kept as a builder so tests can inspect it without hitting a real model.
func unifiedIntentTools() []aimodel.Tool {
	return []aimodel.Tool{
		{
			Type: "function",
			Function: aimodel.FunctionDefinition{
				Name:        UnifiedToolAnswerDirectly,
				Description: "Answer the user's request directly, without invoking any sub-agent. Use this for greetings, general knowledge, small talk, simple math, and anything that does not require project-specific tools.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{
							"type":        "string",
							"description": "The full text to return to the user.",
						},
					},
					"required": []string{"text"},
				},
			},
		},
		{
			Type: "function",
			Function: aimodel.FunctionDefinition{
				Name:        UnifiedToolDelegateTo,
				Description: "Delegate the user's request to a specialist sub-agent (e.g. coder, researcher, reviewer). Use this when the task clearly maps to a single agent's capability.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"agent": map[string]any{
							"type":        "string",
							"description": "The ID of the sub-agent to invoke.",
						},
					},
					"required": []string{"agent"},
				},
			},
		},
		{
			Type: "function",
			Function: aimodel.FunctionDefinition{
				Name:        UnifiedToolPlanTask,
				Description: "Produce a multi-step plan when the task genuinely requires several distinct sub-agent steps. Each step names an agent and lists dependencies.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"goal": map[string]any{
							"type":        "string",
							"description": "The overall objective of the plan.",
						},
						"steps": map[string]any{
							"type":        "array",
							"description": "Ordered list of steps.",
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
				},
			},
		},
	}
}

// answerDirectlyArgs is the parsed arguments for the answer_directly tool call.
type answerDirectlyArgs struct {
	Text string `json:"text"`
}

// delegateToArgs is the parsed arguments for the delegate_to tool call.
type delegateToArgs struct {
	Agent string `json:"agent"`
}

// planTaskArgs is the parsed arguments for the plan_task tool call.
type planTaskArgs struct {
	Goal  string     `json:"goal"`
	Steps []PlanStep `json:"steps"`
}

// recognizeIntentUnified runs a single tool-calling LLM invocation that can
// either answer the user inline or choose a delegation tool. It returns the
// same (intent, contextSummary, usage, error) tuple as the classic
// recognizeIntentDirect so callers are interchangeable.
//
// Behaviour matrix:
//   - LLM picks answer_directly   → IntentResult{Mode:"answered", Answer: text}
//   - LLM picks delegate_to(a)    → IntentResult{Mode:"direct",   Agent: a}
//   - LLM picks plan_task(g,s)    → IntentResult{Mode:"plan",     Plan: ...}
//   - LLM returns plain text      → treated as answer_directly with that text
//   - LLM returns malformed args  → falls back to fallbackIntent()
//
// It never invokes the explorer; the design scopes exploration out of M2.
func (d *Dispatcher) recognizeIntentUnified(ctx context.Context, req *schema.RunRequest) (*IntentResult, string, *aimodel.Usage, error) {
	ctx = debugs.WithAgentName(ctx, "unified_intent")

	sysPrompt := BuildUnifiedIntentSystemPrompt(d.registry, d.workingDir, unifiedChatGuidelines())
	sysPrompt = appendProjectInstructions(sysPrompt, d.projectInstructions)

	msgs := make([]aimodel.Message, 0, len(req.Messages)+1)
	msgs = append(msgs, aimodel.Message{
		Role:    aimodel.RoleSystem,
		Content: aimodel.NewTextContent(sysPrompt),
	})
	msgs = append(msgs, schema.ToAIModelMessages(req.Messages)...)

	chatReq := &aimodel.ChatRequest{
		Model:      d.model,
		Messages:   msgs,
		Tools:      unifiedIntentTools(),
		ToolChoice: "auto",
	}

	resp, err := d.llm.ChatCompletion(ctx, chatReq)
	if err != nil {
		return nil, "", nil, fmt.Errorf("unified intent LLM call: %w", err)
	}

	usage := &resp.Usage

	if len(resp.Choices) == 0 {
		return nil, "", usage, fmt.Errorf("unified intent: empty response")
	}

	choice := resp.Choices[0]
	intent, parseErr := d.parseUnifiedIntentChoice(choice)
	if parseErr != nil {
		// Tool-call parsing failure: log and fall back to the classic routing
		// result so we never block the pipeline on a malformed response.
		slog.Warn("dispatcher: unified intent parse failed, using fallback",
			"error", parseErr)
		return d.fallbackIntent(), "", usage, nil
	}

	if err := intent.validate(d.registry, d.subAgents); err != nil {
		slog.Warn("dispatcher: unified intent validation failed, using fallback",
			"error", err, "mode", intent.Mode, "agent", intent.Agent)
		return d.fallbackIntent(), "", usage, nil
	}

	return intent, "", usage, nil
}

// parseUnifiedIntentChoice decodes a ChatResponse choice into an IntentResult.
// It prefers the first tool call; when none is present it treats the plain
// assistant text as an answer_directly output.
func (d *Dispatcher) parseUnifiedIntentChoice(choice aimodel.Choice) (*IntentResult, error) {
	msg := choice.Message

	if len(msg.ToolCalls) == 0 {
		text := strings.TrimSpace(msg.Content.Text())
		if text == "" {
			return nil, fmt.Errorf("unified intent: no tool call and no text")
		}
		return &IntentResult{Mode: IntentModeAnswered, Answer: text}, nil
	}

	tc := msg.ToolCalls[0]
	name := tc.Function.Name
	args := tc.Function.Arguments

	switch name {
	case UnifiedToolAnswerDirectly:
		var parsed answerDirectlyArgs
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			return nil, fmt.Errorf("parse %s args: %w", name, err)
		}
		text := strings.TrimSpace(parsed.Text)
		if text == "" {
			// Fall back to the assistant's literal content if it was present.
			text = strings.TrimSpace(msg.Content.Text())
		}
		if text == "" {
			return nil, fmt.Errorf("%s returned empty text", name)
		}
		return &IntentResult{Mode: IntentModeAnswered, Answer: text}, nil

	case UnifiedToolDelegateTo:
		var parsed delegateToArgs
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			return nil, fmt.Errorf("parse %s args: %w", name, err)
		}
		agent := strings.TrimSpace(parsed.Agent)
		if agent == "" {
			return nil, fmt.Errorf("%s: empty agent", name)
		}
		return &IntentResult{Mode: "direct", Agent: agent}, nil

	case UnifiedToolPlanTask:
		var parsed planTaskArgs
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			return nil, fmt.Errorf("parse %s args: %w", name, err)
		}
		if len(parsed.Steps) == 0 {
			return nil, fmt.Errorf("%s: empty steps", name)
		}
		return &IntentResult{
			Mode: "plan",
			Plan: &Plan{Goal: parsed.Goal, Steps: parsed.Steps},
		}, nil

	default:
		return nil, fmt.Errorf("unified intent: unexpected tool call %q", name)
	}
}

// unifiedChatGuidelines returns the portion of the chat agent's system prompt
// that still applies when the primary LLM answers directly. It is duplicated
// here (rather than imported from vv/agents) to avoid a cross-package cycle:
// vv/agents imports vv/dispatches indirectly via registries factories.
func unifiedChatGuidelines() string {
	return `1. Be concise but thorough. Provide enough detail to fully answer the question.
2. When uncertain, say so rather than guessing.
3. Use formatting (lists, code blocks) when it improves clarity.
4. If a question is ambiguous, address the most likely interpretation and note alternatives.`
}

// runAnsweredDirect materialises an IntentModeAnswered result into a
// RunResponse carrying the LLM's text as a single assistant message. The
// intent-stage usage is attached so cost/latency accounting matches the
// (collapsed) single-call shape.
func (d *Dispatcher) runAnsweredDirect(req *schema.RunRequest, intent *IntentResult, intentUsage *aimodel.Usage) *schema.RunResponse {
	msg := schema.NewAssistantMessage(
		aimodel.Message{
			Role:    aimodel.RoleAssistant,
			Content: aimodel.NewTextContent(intent.Answer),
		},
		d.ID(),
	)

	return &schema.RunResponse{
		Messages:  []schema.Message{msg},
		SessionID: req.SessionID,
		Usage:     intentUsage,
	}
}

// streamAnsweredDirect emits the unified-intent answer as a streamed
// assistant message and a closing execute-phase bracket so downstream
// consumers see the same event envelope shape they already expect.
func (d *Dispatcher) streamAnsweredDirect(
	send func(schema.Event) error,
	req *schema.RunRequest,
	intent *IntentResult,
	agentID, sessionID string,
) error {
	// Tag the collapsed execution phase so dashboards can count the
	// answer-direct path separately from sub-agent dispatch.
	if err := send(schema.NewEvent(schema.EventPhaseStart, agentID, sessionID, schema.PhaseStartData{
		Phase:      "execute",
		PhaseIndex: 2,
		TotalPhase: 0,
	})); err != nil {
		return err
	}

	start := time.Now()

	// Emit the answer as a text_delta event — the same event shape that
	// streamed LLM tokens take — so stream consumers (CLI, HTTP SSE) render
	// the unified answer with zero extra plumbing.
	if err := send(schema.NewEvent(schema.EventTextDelta, agentID, sessionID, schema.TextDeltaData{
		Delta: intent.Answer,
	})); err != nil {
		return err
	}

	if err := send(schema.NewEvent(schema.EventPhaseEnd, agentID, sessionID, schema.PhaseEndData{
		Phase:    "execute",
		Duration: time.Since(start).Milliseconds(),
		Summary:  unifiedAnsweredSummary,
	})); err != nil {
		return err
	}

	_ = req // retained for signature symmetry and future enrichment (e.g., session-id propagation)
	return nil
}
