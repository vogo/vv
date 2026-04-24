package dispatches_tests

import (
	"context"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/dispatches"
)

// =============================================================================
// Unified-intent integration tests (design M2).
//
// These tests exercise the dispatcher end-to-end against a mock LLM that
// returns pre-canned tool-call responses. They are the acceptance gate for
// the "1 LLM call for answered requests" promise in the design doc.
// =============================================================================

// toolCallChatResponse builds an assistant response containing a single
// function/tool call with the given name and raw arguments JSON.
func toolCallChatResponse(name, argsJSON string) *aimodel.ChatResponse {
	return &aimodel.ChatResponse{
		Choices: []aimodel.Choice{
			{
				Message: aimodel.Message{
					Role: aimodel.RoleAssistant,
					ToolCalls: []aimodel.ToolCall{
						{
							ID:   "tc_unified",
							Type: "function",
							Function: aimodel.FunctionCall{
								Name:      name,
								Arguments: argsJSON,
							},
						},
					},
				},
				FinishReason: aimodel.FinishReasonToolCalls,
			},
		},
	}
}

// newUnifiedIntentDispatcher wires a Dispatcher with unified intent enabled,
// fast-path disabled (so it cannot shadow the unified call), and the given
// LLM and sub-agents.
func newUnifiedIntentDispatcher(
	t *testing.T,
	chat, coder agent.Agent,
	mockLLM aimodel.ChatCompleter,
) *dispatches.Dispatcher {
	t.Helper()

	reg := newIntegrationRegistry()

	subAgents := makeSubAgents(map[string]agent.Agent{
		"chat":  chat,
		"coder": coder,
	})

	return dispatches.New(
		reg, subAgents, nil, nil, nil,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithSummaryPolicy(dispatches.SummaryNever),
		dispatches.WithFastPath(dispatches.DisabledFastPathConfig()),
		dispatches.WithUnifiedIntent(true),
	)
}

// TestUnifiedIntent_AnswerDirectly_OneLLMCall is the core acceptance test for
// M2: when the model picks answer_directly, the user-facing response arrives
// in exactly one LLM call, and no sub-agent runs.
func TestUnifiedIntent_AnswerDirectly_OneLLMCall(t *testing.T) {
	chat := &callTrackingAgent{id: "chat"}
	coder := &callTrackingAgent{id: "coder"}

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			toolCallChatResponse(dispatches.UnifiedToolAnswerDirectly, `{"text":"Hi there! What can I help with?"}`),
		},
	}

	d := newUnifiedIntentDispatcher(t, chat, coder, mockLLM)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hi")},
		SessionID: "ui-ac-1",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := int(mockLLM.callCount.Load()); got != 1 {
		t.Errorf("LLM call count = %d, want 1 for answered path (M2 core promise)", got)
	}

	if chat.called.Load() {
		t.Error("chat sub-agent must not run when unified intent answered directly")
	}

	if coder.called.Load() {
		t.Error("coder sub-agent must not run for a greeting")
	}

	if len(resp.Messages) == 0 || !strings.Contains(resp.Messages[0].Content.Text(), "Hi there") {
		t.Errorf("unexpected response: %+v", resp.Messages)
	}
}

// TestUnifiedIntent_DelegateTo_UsesClassicDirectFlow verifies that picking
// delegate_to forwards the request to the named sub-agent, preserving the
// existing direct-dispatch behaviour observable to stream consumers.
func TestUnifiedIntent_DelegateTo_UsesClassicDirectFlow(t *testing.T) {
	chat := &callTrackingAgent{id: "chat"}
	coder := &callTrackingAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("here is your code"),
				}, "coder"),
			},
		},
	}

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			toolCallChatResponse(dispatches.UnifiedToolDelegateTo, `{"agent":"coder"}`),
		},
	}

	d := newUnifiedIntentDispatcher(t, chat, coder, mockLLM)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("write a hello world")},
		SessionID: "ui-ac-2",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := int(mockLLM.callCount.Load()); got != 1 {
		t.Errorf("unified intent must still be 1 LLM call for delegate_to (got %d)", got)
	}

	if !coder.called.Load() {
		t.Error("coder agent should have been invoked by delegate_to path")
	}

	if chat.called.Load() {
		t.Error("chat must not run when delegate_to names coder")
	}

	if len(resp.Messages) == 0 || !strings.Contains(resp.Messages[0].Content.Text(), "here is your code") {
		t.Errorf("unexpected delegated response: %+v", resp.Messages)
	}
}

// TestUnifiedIntent_PlanTask_RunsPlanDAG verifies that plan_task produces the
// same DAG execution as the classic plan mode.
func TestUnifiedIntent_PlanTask_RunsPlanDAG(t *testing.T) {
	chat := &callTrackingAgent{id: "chat"}
	coder := &callTrackingAgent{id: "coder"}

	args := `{"goal":"two-step","steps":[` +
		`{"id":"s1","description":"research","agent":"researcher","depends_on":[]},` +
		`{"id":"s2","description":"implement","agent":"coder","depends_on":["s1"]}` +
		`]}`

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			toolCallChatResponse(dispatches.UnifiedToolPlanTask, args),
		},
	}

	d := newUnifiedIntentDispatcher(t, chat, coder, mockLLM)

	if _, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("plan and build a feature")},
		SessionID: "ui-ac-3",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !coder.called.Load() {
		t.Error("plan_task DAG should have invoked coder step")
	}
}

// TestUnifiedIntent_StreamEmitsUnifiedIntentPhase verifies the streaming path
// emits Phase="unified_intent" (not "intent") when the flag is on, and that
// the answer lands as a text_delta without a sub-agent start/end envelope.
func TestUnifiedIntent_StreamEmitsUnifiedIntentPhase(t *testing.T) {
	chat := &callTrackingAgent{id: "chat"}
	coder := &callTrackingAgent{id: "coder"}

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			toolCallChatResponse(dispatches.UnifiedToolAnswerDirectly, `{"text":"stream answered"}`),
		},
	}

	d := newUnifiedIntentDispatcher(t, chat, coder, mockLLM)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello there")},
		SessionID: "ui-ac-4",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var (
		sawUnifiedStart bool
		sawUnifiedEnd   bool
		textDelta       string
		sawSubAgent     bool
	)

	for {
		ev, err := stream.Recv()
		if err != nil {
			break
		}
		switch ev.Type {
		case schema.EventPhaseStart:
			if d, ok := ev.Data.(schema.PhaseStartData); ok && d.Phase == dispatches.UnifiedIntentPhase {
				sawUnifiedStart = true
			}
		case schema.EventPhaseEnd:
			if d, ok := ev.Data.(schema.PhaseEndData); ok && d.Phase == dispatches.UnifiedIntentPhase {
				sawUnifiedEnd = true
			}
		case schema.EventTextDelta:
			if d, ok := ev.Data.(schema.TextDeltaData); ok {
				textDelta += d.Delta
			}
		case schema.EventSubAgentStart, schema.EventSubAgentEnd:
			sawSubAgent = true
		}
	}

	if !sawUnifiedStart || !sawUnifiedEnd {
		t.Errorf("expected phase_start/end for unified_intent, sawStart=%v sawEnd=%v", sawUnifiedStart, sawUnifiedEnd)
	}

	if !strings.Contains(textDelta, "stream answered") {
		t.Errorf("text delta stream missing answer: got %q", textDelta)
	}

	if sawSubAgent {
		t.Error("answered path must not emit sub_agent events")
	}

	if chat.called.Load() || coder.called.Load() {
		t.Error("no sub-agent must run on the answered stream path")
	}
}

// TestUnifiedIntent_Disabled_RunsClassicIntent verifies the flag-off path is
// unchanged: the classic two-call flow (intent JSON → sub-agent) still works.
func TestUnifiedIntent_Disabled_RunsClassicIntent(t *testing.T) {
	chat := &callTrackingAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("classic chat answer"),
				}, "chat"),
			},
		},
	}

	// Classic flow expects plain-JSON intent response, then sub-agent call.
	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{Choices: []aimodel.Choice{{
				Message: aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent(`{"needs_exploration":false,"mode":"direct","agent":"chat"}`),
				},
			}}},
		},
	}

	reg := newIntegrationRegistry()
	subAgents := makeSubAgents(map[string]agent.Agent{"chat": chat})

	d := dispatches.New(
		reg, subAgents, nil, nil, nil,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
		dispatches.WithSummaryPolicy(dispatches.SummaryNever),
		dispatches.WithFastPath(dispatches.DisabledFastPathConfig()),
		// explicitly omit WithUnifiedIntent → flag remains false
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hi there, classic path")},
		SessionID: "ui-ac-5",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !chat.called.Load() {
		t.Error("flag-off path must still dispatch to chat sub-agent")
	}

	if len(resp.Messages) == 0 || !strings.Contains(resp.Messages[0].Content.Text(), "classic chat answer") {
		t.Errorf("unexpected classic-flow response: %+v", resp.Messages)
	}
}
