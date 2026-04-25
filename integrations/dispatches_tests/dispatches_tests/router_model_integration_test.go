package dispatches_tests

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/dispatches"
)

// =============================================================================
// Router model (design M3) integration tests.
//
// These tests run two independent mock LLMs side-by-side and assert that
// dispatcher routing/classification calls land on the router mock while the
// main LLM mock stays untouched (sub-agent stubs never reach it in these
// scenarios). They are the acceptance gate for the "router model only
// handles routing, main model only handles execution" promise.
// =============================================================================

// countingMockLLM is a sequentialMockLLM that also exposes a tag so tests can
// pin the identity of the client a call reached. Implemented locally because
// the shared helper has no tag field.
type countingMockLLM struct {
	tag       string
	responses []*aimodel.ChatResponse
	callCount atomic.Int32
}

func (m *countingMockLLM) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	idx := int(m.callCount.Add(1)) - 1
	if idx < len(m.responses) {
		return m.responses[idx], nil
	}
	if len(m.responses) > 0 {
		return m.responses[len(m.responses)-1], nil
	}
	return &aimodel.ChatResponse{}, nil
}

func (m *countingMockLLM) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, nil
}

// newRouterModelDispatcher wires a Dispatcher with both a main and router LLM
// and unified_intent enabled (so the router is exercised on the tool-calling
// path). Fast path is disabled so it cannot shadow the routing call.
func newRouterModelDispatcher(
	t *testing.T,
	mainLLM, routerLLM aimodel.ChatCompleter,
	subAgents map[string]agent.Agent,
) *dispatches.Dispatcher {
	t.Helper()

	return dispatches.New(
		newIntegrationRegistry(),
		subAgents, nil, nil, nil,
		dispatches.WithLLM(mainLLM, "main-model"),
		dispatches.WithRouterLLM(routerLLM, "router-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithSummaryPolicy(dispatches.SummaryNever),
		dispatches.WithFastPath(dispatches.DisabledFastPathConfig()),
		dispatches.WithUnifiedIntent(true),
	)
}

// TestRouterModel_AnsweredUsesOnlyRouter — unified-intent answer_directly
// produces exactly one router LLM call and zero main-LLM calls, and no
// sub-agents run.
func TestRouterModel_AnsweredUsesOnlyRouter(t *testing.T) {
	mainLLM := &countingMockLLM{tag: "main"}
	routerLLM := &countingMockLLM{
		tag: "router",
		responses: []*aimodel.ChatResponse{
			toolCallChatResponse(dispatches.UnifiedToolAnswerDirectly, `{"text":"routed greeting"}`),
		},
	}

	chat := &callTrackingAgent{id: "chat"}
	coder := &callTrackingAgent{id: "coder"}

	d := newRouterModelDispatcher(t, mainLLM, routerLLM, makeSubAgents(map[string]agent.Agent{
		"chat":  chat,
		"coder": coder,
	}))

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello there")},
		SessionID: "router-1",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := int(routerLLM.callCount.Load()); got != 1 {
		t.Errorf("router.calls = %d, want 1", got)
	}
	if got := int(mainLLM.callCount.Load()); got != 0 {
		t.Errorf("main.calls = %d, want 0 (routing must not hit main LLM)", got)
	}
	if chat.called.Load() || coder.called.Load() {
		t.Error("no sub-agent should run on answered path")
	}
	if len(resp.Messages) == 0 || !strings.Contains(resp.Messages[0].Content.Text(), "routed greeting") {
		t.Errorf("unexpected response: %+v", resp.Messages)
	}
}

// TestRouterModel_DelegateIsolatesRoutingFromExecution — unified-intent
// delegate_to("coder") still goes through the router (one router call), and
// the main LLM is never touched by the dispatcher itself (sub-agent stub does
// not invoke an LLM).
func TestRouterModel_DelegateIsolatesRoutingFromExecution(t *testing.T) {
	mainLLM := &countingMockLLM{tag: "main"}
	routerLLM := &countingMockLLM{
		tag: "router",
		responses: []*aimodel.ChatResponse{
			toolCallChatResponse(dispatches.UnifiedToolDelegateTo, `{"agent":"coder"}`),
		},
	}

	chat := &callTrackingAgent{id: "chat"}
	coder := &callTrackingAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("delegated code result"),
				}, "coder"),
			},
		},
	}

	d := newRouterModelDispatcher(t, mainLLM, routerLLM, makeSubAgents(map[string]agent.Agent{
		"chat":  chat,
		"coder": coder,
	}))

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("write code")},
		SessionID: "router-2",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := int(routerLLM.callCount.Load()); got != 1 {
		t.Errorf("router.calls = %d, want 1 (delegate_to still uses router)", got)
	}
	if got := int(mainLLM.callCount.Load()); got != 0 {
		t.Errorf("main.calls = %d, want 0 (dispatcher must not call main LLM directly)", got)
	}
	if !coder.called.Load() {
		t.Error("coder sub-agent should have been invoked by delegate_to")
	}
	if chat.called.Load() {
		t.Error("chat must not run when delegate_to names coder")
	}
	if len(resp.Messages) == 0 || !strings.Contains(resp.Messages[0].Content.Text(), "delegated code result") {
		t.Errorf("unexpected response: %+v", resp.Messages)
	}
}

// TestRouterModel_Disabled_UsesMain — leaving the router unset preserves the
// pre-M3 single-LLM shape: the main LLM receives the unified-intent call.
func TestRouterModel_Disabled_UsesMain(t *testing.T) {
	mainLLM := &countingMockLLM{
		tag: "main",
		responses: []*aimodel.ChatResponse{
			toolCallChatResponse(dispatches.UnifiedToolAnswerDirectly, `{"text":"no-router answer"}`),
		},
	}

	chat := &callTrackingAgent{id: "chat"}
	coder := &callTrackingAgent{id: "coder"}

	d := dispatches.New(
		newIntegrationRegistry(),
		makeSubAgents(map[string]agent.Agent{"chat": chat, "coder": coder}),
		nil, nil, nil,
		dispatches.WithLLM(mainLLM, "main-model"),
		// No WithRouterLLM.
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithSummaryPolicy(dispatches.SummaryNever),
		dispatches.WithFastPath(dispatches.DisabledFastPathConfig()),
		dispatches.WithUnifiedIntent(true),
	)

	if _, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hi")},
		SessionID: "router-3",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := int(mainLLM.callCount.Load()); got != 1 {
		t.Errorf("without router, main.calls = %d, want 1 (unified intent must go through main)", got)
	}
}
