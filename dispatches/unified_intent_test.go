package dispatches

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

// newUnifiedDispatcher builds a Dispatcher wired for the unified-intent path
// with a counting mock LLM the test can pre-seed with a ChatResponse.
func newUnifiedDispatcher(t *testing.T, llm aimodel.ChatCompleter) (*Dispatcher, map[string]*stubAgent) {
	t.Helper()

	reg := newTestRegistry()

	stubs := map[string]*stubAgent{
		"chat":       {id: "chat"},
		"coder":      {id: "coder"},
		"researcher": {id: "researcher"},
		"reviewer":   {id: "reviewer"},
	}
	subAgents := map[string]agent.Agent{}
	for k, v := range stubs {
		subAgents[k] = v
	}

	d := New(
		reg,
		subAgents,
		nil, // no explorer
		nil, // no planner (keeps us on the unified path)
		nil,
		WithLLM(llm, "test-model"),
		WithFallbackAgent(stubs["chat"]),
		WithUnifiedIntent(true),
		WithFastPath(DisabledFastPathConfig()), // disable fast path for these tests
	)

	return d, stubs
}

func toolCallResponse(name, args string) *aimodel.ChatResponse {
	return &aimodel.ChatResponse{
		Choices: []aimodel.Choice{
			{
				Message: aimodel.Message{
					Role: aimodel.RoleAssistant,
					ToolCalls: []aimodel.ToolCall{
						{
							ID:   "tc_1",
							Type: "function",
							Function: aimodel.FunctionCall{
								Name:      name,
								Arguments: args,
							},
						},
					},
				},
				FinishReason: aimodel.FinishReasonToolCalls,
			},
		},
	}
}

func plainTextResponse(text string) *aimodel.ChatResponse {
	return &aimodel.ChatResponse{
		Choices: []aimodel.Choice{
			{
				Message: aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent(text),
				},
				FinishReason: aimodel.FinishReasonStop,
			},
		},
	}
}

func TestUnifiedIntent_AnswerDirectly_SingleLLMCall(t *testing.T) {
	llm := &countingChatCompleter{
		response: toolCallResponse(UnifiedToolAnswerDirectly, `{"text":"Hello! How can I help?"}`),
	}

	d, stubs := newUnifiedDispatcher(t, llm)

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("hi")}}

	resp, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if llm.calls != 1 {
		t.Fatalf("expected exactly 1 LLM call for answered path, got %d", llm.calls)
	}

	if stubs["chat"].ranCount() != 0 {
		t.Fatalf("expected chat sub-agent not to run when answered directly, got %d runs", stubs["chat"].ranCount())
	}

	if len(resp.Messages) != 1 || !strings.Contains(resp.Messages[0].Content.Text(), "How can I help") {
		t.Fatalf("unexpected answer payload: %+v", resp.Messages)
	}
}

func TestUnifiedIntent_DelegateTo_RoutesToSubAgent(t *testing.T) {
	llm := &countingChatCompleter{
		response: toolCallResponse(UnifiedToolDelegateTo, `{"agent":"coder"}`),
	}

	d, stubs := newUnifiedDispatcher(t, llm)
	stubs["coder"].response = &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("delegated output"),
			}, "coder"),
		},
	}

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("write me code")}}

	resp, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stubs["coder"].ranCount() != 1 {
		t.Fatalf("expected coder to run exactly once, got %d", stubs["coder"].ranCount())
	}

	if len(resp.Messages) == 0 || !strings.Contains(resp.Messages[0].Content.Text(), "delegated output") {
		t.Fatalf("unexpected delegated response: %+v", resp.Messages)
	}
}

func TestUnifiedIntent_PlainText_TreatedAsAnswered(t *testing.T) {
	llm := &countingChatCompleter{
		response: plainTextResponse("some model forgot to call a tool"),
	}

	d, stubs := newUnifiedDispatcher(t, llm)

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("hi")}}

	resp, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stubs["chat"].ranCount() != 0 {
		t.Fatalf("expected no sub-agent for plain-text fallback, got chat ran %d times", stubs["chat"].ranCount())
	}

	if len(resp.Messages) != 1 || !strings.Contains(resp.Messages[0].Content.Text(), "forgot to call") {
		t.Fatalf("unexpected answer payload: %+v", resp.Messages)
	}
}

func TestUnifiedIntent_MalformedArgs_FallsBackToChat(t *testing.T) {
	llm := &countingChatCompleter{
		response: toolCallResponse(UnifiedToolAnswerDirectly, `{not-json}`),
	}

	d, stubs := newUnifiedDispatcher(t, llm)
	stubs["chat"].response = &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("fallback text"),
			}, "chat"),
		},
	}

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("hi")}}

	resp, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stubs["chat"].ranCount() != 1 {
		t.Fatalf("expected chat fallback to run once on parse failure, got %d", stubs["chat"].ranCount())
	}

	if len(resp.Messages) == 0 || !strings.Contains(resp.Messages[0].Content.Text(), "fallback text") {
		t.Fatalf("unexpected fallback payload: %+v", resp.Messages)
	}
}

func TestUnifiedIntent_UnknownAgent_FallsBackToChat(t *testing.T) {
	llm := &countingChatCompleter{
		response: toolCallResponse(UnifiedToolDelegateTo, `{"agent":"ghost"}`),
	}

	d, stubs := newUnifiedDispatcher(t, llm)
	stubs["chat"].response = &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("fallback"),
			}, "chat"),
		},
	}

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("hi")}}

	if _, err := d.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stubs["chat"].ranCount() != 1 {
		t.Fatalf("expected fallback chat to run when delegate target is unknown, got %d", stubs["chat"].ranCount())
	}
}

func TestUnifiedIntent_PlanTask_RoutesToDAG(t *testing.T) {
	args := `{"goal":"two-step","steps":[` +
		`{"id":"s1","description":"do a","agent":"researcher","depends_on":[]},` +
		`{"id":"s2","description":"do b","agent":"coder","depends_on":["s1"]}` +
		`]}`

	llm := &countingChatCompleter{
		response: toolCallResponse(UnifiedToolPlanTask, args),
	}

	d, stubs := newUnifiedDispatcher(t, llm)

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("complex task")}}

	if _, err := d.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stubs["researcher"].ranCount() == 0 || stubs["coder"].ranCount() == 0 {
		t.Fatalf("expected both plan steps to run; researcher=%d coder=%d",
			stubs["researcher"].ranCount(), stubs["coder"].ranCount())
	}
}

func TestUnifiedIntent_LLMError_Bubbles(t *testing.T) {
	wantErr := errors.New("upstream is angry")
	llm := &countingChatCompleter{err: wantErr}

	d, stubs := newUnifiedDispatcher(t, llm)
	stubs["chat"].response = &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("fallback"),
			}, "chat"),
		},
	}

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("hi")}}

	// Current contract: recognizeIntent failure falls back to chat agent via
	// Dispatcher.Run (preserves pre-M2 behaviour). Tests assert the fallback
	// actually runs; the original error is logged but not returned.
	resp, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("expected fallback, got error: %v", err)
	}

	if stubs["chat"].ranCount() != 1 {
		t.Fatalf("expected chat fallback on LLM error, got %d runs", stubs["chat"].ranCount())
	}

	if len(resp.Messages) == 0 {
		t.Fatalf("expected fallback response content")
	}
}

func TestUnifiedIntent_Disabled_RunsClassicIntent(t *testing.T) {
	// A tool-call response would only be produced when unified mode passes
	// tools on the wire. With the flag off, the dispatcher must not hand
	// off to the unified parser — instead it runs recognizeIntentDirect's
	// JSON path.
	llm := &countingChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{Message: aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent(`{"needs_exploration":false,"mode":"direct","agent":"chat"}`),
				}},
			},
		},
	}

	reg := newTestRegistry()
	stubChat := &stubAgent{id: "chat"}
	subAgents := map[string]agent.Agent{"chat": stubChat, "coder": &stubAgent{id: "coder"}}

	d := New(
		reg,
		subAgents,
		nil, nil, nil,
		WithLLM(llm, "test-model"),
		WithFallbackAgent(stubChat),
		// explicitly omit WithUnifiedIntent → flag stays false
		WithFastPath(DisabledFastPathConfig()),
	)

	if _, err := d.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("hi")},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stubChat.ranCount() != 1 {
		t.Fatalf("flag-off path should dispatch to chat sub-agent, got %d runs", stubChat.ranCount())
	}
}

func TestUnifiedIntent_ToolSchema_Shape(t *testing.T) {
	tools := unifiedIntentTools()
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Function.Name] = true
		if tool.Type != "function" {
			t.Errorf("tool %q has non-function type %q", tool.Function.Name, tool.Type)
		}
		if tool.Function.Parameters == nil {
			t.Errorf("tool %q missing parameters schema", tool.Function.Name)
		}
	}

	for _, want := range []string{UnifiedToolAnswerDirectly, UnifiedToolDelegateTo, UnifiedToolPlanTask} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}
