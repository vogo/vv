package dispatches

import (
	"context"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

// taggingChatCompleter stamps its tag onto every request it receives via the
// returned response's first choice Content. Used by router tests to prove a
// given call landed on the router client (tag="router") vs the main client
// (tag="main").
type taggingChatCompleter struct {
	tag      string
	calls    int
	response *aimodel.ChatResponse
	err      error
}

func (c *taggingChatCompleter) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return c.response, nil
}

func (c *taggingChatCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, nil
}

func TestRouterLLM_FallsBackToMainWhenUnset(t *testing.T) {
	main := &taggingChatCompleter{tag: "main"}

	d := New(
		newTestRegistry(),
		map[string]agent.Agent{"chat": &stubAgent{id: "chat"}},
		nil, nil, nil,
		WithLLM(main, "main-model"),
	)

	if d.routerClient() != main {
		t.Error("routerClient should return main LLM when router is unset")
	}
	if d.routerModelName() != "main-model" {
		t.Errorf("routerModelName = %q, want main-model", d.routerModelName())
	}
}

func TestWithRouterLLM_NilIsIdempotent(t *testing.T) {
	main := &taggingChatCompleter{tag: "main"}

	d := New(
		newTestRegistry(),
		map[string]agent.Agent{"chat": &stubAgent{id: "chat"}},
		nil, nil, nil,
		WithLLM(main, "main-model"),
		WithRouterLLM(nil, "ignored"),
		WithRouterLLM(&taggingChatCompleter{tag: "router"}, ""), // empty model also ignored
	)

	if d.routerClient() != main {
		t.Error("nil/empty WithRouterLLM calls must not install a router client")
	}
	if d.routerModelName() != "main-model" {
		t.Errorf("routerModelName = %q, want main-model", d.routerModelName())
	}
}

func TestWithRouterLLM_SetsBothFields(t *testing.T) {
	main := &taggingChatCompleter{tag: "main"}
	router := &taggingChatCompleter{tag: "router"}

	d := New(
		newTestRegistry(),
		map[string]agent.Agent{"chat": &stubAgent{id: "chat"}},
		nil, nil, nil,
		WithLLM(main, "main-model"),
		WithRouterLLM(router, "router-model"),
	)

	if d.routerClient() != router {
		t.Error("routerClient should return the dedicated router client when set")
	}
	if d.routerModelName() != "router-model" {
		t.Errorf("routerModelName = %q, want router-model", d.routerModelName())
	}
}

// TestRouterLLM_UnifiedIntentUsesRouter — unified-intent path (M2) must route
// its single LLM call through the router client when configured.
func TestRouterLLM_UnifiedIntentUsesRouter(t *testing.T) {
	main := &taggingChatCompleter{tag: "main"}
	router := &taggingChatCompleter{
		tag:      "router",
		response: toolCallResponse(UnifiedToolAnswerDirectly, `{"text":"hi from router"}`),
	}

	reg := newTestRegistry()
	chat := &stubAgent{id: "chat"}
	subAgents := map[string]agent.Agent{
		"chat":       chat,
		"coder":      &stubAgent{id: "coder"},
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
	}

	d := New(
		reg, subAgents, nil, nil, nil,
		WithLLM(main, "main-model"),
		WithRouterLLM(router, "router-model"),
		WithFallbackAgent(chat),
		WithUnifiedIntent(true),
		WithFastPath(DisabledFastPathConfig()),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if router.calls != 1 {
		t.Errorf("router.calls = %d, want 1 (unified intent must hit router)", router.calls)
	}
	if main.calls != 0 {
		t.Errorf("main.calls = %d, want 0 (main LLM must not receive routing calls)", main.calls)
	}
	if chat.ranCount() != 0 {
		t.Errorf("chat must not run on answer_directly path; ran %d times", chat.ranCount())
	}
	if len(resp.Messages) == 0 || !strings.Contains(resp.Messages[0].Content.Text(), "hi from router") {
		t.Errorf("unexpected response: %+v", resp.Messages)
	}
}

// TestRouterLLM_ClassicDirectUsesRouter — classic recognizeIntentDirect path
// (no planner, no unified-intent) must also route through the router client.
func TestRouterLLM_ClassicDirectUsesRouter(t *testing.T) {
	main := &taggingChatCompleter{tag: "main"}
	router := &taggingChatCompleter{
		tag: "router",
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{{
				Message: aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent(`{"needs_exploration":false,"mode":"direct","agent":"chat"}`),
				},
			}},
		},
	}

	reg := newTestRegistry()
	chat := &stubAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("classic answer"),
				}, "chat"),
			},
		},
	}

	d := New(
		reg,
		map[string]agent.Agent{
			"chat":       chat,
			"coder":      &stubAgent{id: "coder"},
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
		},
		nil, nil, nil,
		WithLLM(main, "main-model"),
		WithRouterLLM(router, "router-model"),
		WithFallbackAgent(chat),
		WithFastPath(DisabledFastPathConfig()),
	)

	if _, err := d.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("simple question")},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if router.calls != 1 {
		t.Errorf("router.calls = %d, want 1 (recognizeIntentDirect must hit router)", router.calls)
	}
	if main.calls != 0 {
		t.Errorf("main.calls = %d, want 0 (main LLM must not receive routing calls)", main.calls)
	}
	if chat.ranCount() != 1 {
		t.Errorf("chat sub-agent should have run once, got %d", chat.ranCount())
	}
}

// TestRouterLLM_ReassessUsesRouter — after explorer runs, reassessIntent is a
// routing decision too and must hit the router client.
func TestRouterLLM_ReassessUsesRouter(t *testing.T) {
	main := &taggingChatCompleter{tag: "main"}

	// Router returns needs_exploration=true the first time, then a direct
	// routing decision on the reassess pass.
	router := &sequentialTaggingCompleter{
		tag: "router",
		responses: []*aimodel.ChatResponse{
			{Choices: []aimodel.Choice{{Message: aimodel.Message{
				Role: aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(
					`{"needs_exploration":true}`),
			}}}},
			{Choices: []aimodel.Choice{{Message: aimodel.Message{
				Role: aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(
					`{"needs_exploration":false,"mode":"direct","agent":"chat"}`),
			}}}},
		},
	}

	reg := newTestRegistry()
	chat := &stubAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("reassess answer"),
				}, "chat"),
			},
		},
	}
	explorer := &stubAgent{
		id: "explorer",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("explorer context"),
				}, "explorer"),
			},
		},
	}

	d := New(
		reg,
		map[string]agent.Agent{
			"chat":       chat,
			"coder":      &stubAgent{id: "coder"},
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
		},
		explorer, nil, nil,
		WithLLM(main, "main-model"),
		WithRouterLLM(router, "router-model"),
		WithFallbackAgent(chat),
		WithFastPath(DisabledFastPathConfig()),
	)

	if _, err := d.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("needs explore")},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if router.calls != 2 {
		t.Errorf("router.calls = %d, want 2 (intent + reassess both go through router)", router.calls)
	}
	if main.calls != 0 {
		t.Errorf("main.calls = %d, want 0 (main LLM must not receive routing calls)", main.calls)
	}
}

// sequentialTaggingCompleter returns preloaded responses in order, counting
// calls. Shared shape with fastpath_test.go's countingChatCompleter but multi-
// response so reassess and other two-call flows can be driven from a single
// mock.
type sequentialTaggingCompleter struct {
	tag       string
	calls     int
	responses []*aimodel.ChatResponse
	err       error
}

func (c *sequentialTaggingCompleter) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	if c.err != nil {
		return nil, c.err
	}
	idx := c.calls
	c.calls++
	if idx < len(c.responses) {
		return c.responses[idx], nil
	}
	if len(c.responses) > 0 {
		return c.responses[len(c.responses)-1], nil
	}
	return &aimodel.ChatResponse{}, nil
}

func (c *sequentialTaggingCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, nil
}
