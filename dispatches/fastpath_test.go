package dispatches

import (
	"context"
	"errors"
	"io"
	"regexp"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

// countingChatCompleter counts ChatCompletion invocations. Useful for asserting
// that the fast-path skipped the intent LLM call.
type countingChatCompleter struct {
	calls    int
	response *aimodel.ChatResponse
	err      error
}

func (c *countingChatCompleter) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	c.calls++

	if c.err != nil {
		return nil, c.err
	}

	return c.response, nil
}

func (c *countingChatCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, errors.New("not implemented")
}

// newFastPathDispatcher builds a Dispatcher with a minimal set of stub sub-agents.
func newFastPathDispatcher(t *testing.T, fp FastPathConfig) (*Dispatcher, *stubAgent, *stubAgent, *countingChatCompleter) {
	t.Helper()

	reg := newTestRegistry()
	chat := &stubAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("hi there"),
				}, "chat"),
			},
		},
	}
	coder := &stubAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("15625"),
				}, "coder"),
			},
		},
	}

	llm := &countingChatCompleter{}

	d := New(
		reg,
		map[string]agent.Agent{
			"chat":       chat,
			"coder":      coder,
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
		},
		nil, nil, nil,
		WithLLM(llm, "test-model"),
		WithFallbackAgent(chat),
		WithFastPath(fp),
	)

	return d, chat, coder, llm
}

func TestFastPathClassify_GreetingHitsChat(t *testing.T) {
	d, _, _, _ := newFastPathDispatcher(t, DefaultFastPathConfig())

	cases := []string{"hello", "Hello", "HI", "hey there", "thanks", "thank you for that", "bye"}

	for _, input := range cases {
		req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage(input)}}
		hit := d.fastPathClassify(req)

		if !hit.Hit {
			t.Errorf("%q: expected hit, got miss", input)
			continue
		}

		if hit.Agent != "chat" {
			t.Errorf("%q: agent = %q, want chat", input, hit.Agent)
		}

		if hit.Category != FastPathCategoryGreeting {
			t.Errorf("%q: category = %q, want greeting", input, hit.Category)
		}
	}
}

// TestFastPathClassify_GreetingLateBindsFallback pins the M6 G6 contract:
// DefaultFastPathConfig() leaves the greeting-rule Agent empty so it
// late-binds to whichever fallback agent the Dispatcher carries — no
// hard-coded "chat" string in production code. Swapping the fallback ID
// changes the hit.Agent reported to consumers.
func TestFastPathClassify_GreetingLateBindsFallback(t *testing.T) {
	reg := newTestRegistry()
	fallback := &stubAgent{id: "primary-fallback"}

	d := New(
		reg,
		map[string]agent.Agent{"coder": &stubAgent{id: "coder"}},
		nil, nil, nil,
		WithFallbackAgent(fallback),
		WithFastPath(DefaultFastPathConfig()),
	)

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("hello")}}

	hit := d.fastPathClassify(req)
	if !hit.Hit {
		t.Fatalf("expected greeting hit, got miss: %+v", hit)
	}

	if hit.Agent != "primary-fallback" {
		t.Errorf("hit.Agent = %q, want %q (late-bound to fallback)", hit.Agent, "primary-fallback")
	}

	if hit.Category != FastPathCategoryGreeting {
		t.Errorf("hit.Category = %q, want %q", hit.Category, FastPathCategoryGreeting)
	}
}

func TestFastPathClassify_ChineseGreetings(t *testing.T) {
	d, _, _, _ := newFastPathDispatcher(t, DefaultFastPathConfig())

	cases := []string{"你好", "您好", "在吗", "哈喽", "再见"}

	for _, input := range cases {
		req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage(input)}}
		if hit := d.fastPathClassify(req); !hit.Hit || hit.Agent != "chat" {
			t.Errorf("%q: expected chat hit, got %+v", input, hit)
		}
	}
}

func TestFastPathClassify_ToolTriggerHitsCoder(t *testing.T) {
	d, _, _, _ := newFastPathDispatcher(t, DefaultFastPathConfig())

	cases := []string{"calc 5^6", "date", "pwd", "ls /tmp", "echo hi", "whoami"}

	for _, input := range cases {
		req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage(input)}}
		hit := d.fastPathClassify(req)

		if !hit.Hit {
			t.Errorf("%q: expected hit", input)
			continue
		}

		if hit.Agent != "coder" {
			t.Errorf("%q: agent = %q, want coder", input, hit.Agent)
		}

		if hit.Category != FastPathCategoryToolTrigger {
			t.Errorf("%q: category = %q, want tool_trigger", input, hit.Category)
		}
	}
}

func TestFastPathClassify_DeclinesWhenDisabled(t *testing.T) {
	d, _, _, _ := newFastPathDispatcher(t, DisabledFastPathConfig())

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("hello")}}
	if hit := d.fastPathClassify(req); hit.Hit {
		t.Fatalf("expected miss when disabled, got %+v", hit)
	}
}

func TestFastPathClassify_DeclinesWhenLong(t *testing.T) {
	d, _, _, _ := newFastPathDispatcher(t, DefaultFastPathConfig())

	long := "hello, " + string(make([]byte, 70)) // well over 60 chars
	for i := range []byte(long)[7:] {
		_ = i
	}

	input := "hello there! this is a longer message that exceeds the configured rune cap of sixty chars"
	if len(input) <= DefaultFastPathMaxChars {
		t.Fatalf("test fixture too short: len=%d", len(input))
	}

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage(input)}}
	if hit := d.fastPathClassify(req); hit.Hit {
		t.Fatalf("expected miss for long input, got hit=%+v", hit)
	}
}

func TestFastPathClassify_WordBoundary(t *testing.T) {
	d, _, _, _ := newFastPathDispatcher(t, DefaultFastPathConfig())

	// "helloworld" does not have a boundary after "hello" — must not match.
	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("helloworld")}}
	if hit := d.fastPathClassify(req); hit.Hit {
		t.Fatalf("expected miss for %q, got hit=%+v", "helloworld", hit)
	}
}

func TestFastPathClassify_DeclinesWhenMultiTurn(t *testing.T) {
	d, _, _, _ := newFastPathDispatcher(t, DefaultFastPathConfig())

	req := &schema.RunRequest{
		Messages: []schema.Message{
			schema.NewUserMessage("earlier question"),
			schema.NewAssistantMessage(aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent("earlier answer")}, "chat"),
			schema.NewUserMessage("thanks"),
		},
	}

	if hit := d.fastPathClassify(req); hit.Hit {
		t.Fatalf("expected miss for multi-turn context, got %+v", hit)
	}
}

func TestFastPathClassify_DeclinesWhenToolHistoryPresent(t *testing.T) {
	d, _, _, _ := newFastPathDispatcher(t, DefaultFastPathConfig())

	toolCall := schema.NewAssistantMessage(aimodel.Message{
		Role:      aimodel.RoleAssistant,
		Content:   aimodel.NewTextContent(""),
		ToolCalls: []aimodel.ToolCall{{ID: "1", Function: aimodel.FunctionCall{Name: "bash", Arguments: "{}"}}},
	}, "coder")

	req := &schema.RunRequest{
		Messages: []schema.Message{toolCall, schema.NewUserMessage("thanks")},
	}

	if hit := d.fastPathClassify(req); hit.Hit {
		t.Fatalf("expected miss when tool history present, got %+v", hit)
	}
}

func TestFastPathClassify_DeclinesWhenEmpty(t *testing.T) {
	d, _, _, _ := newFastPathDispatcher(t, DefaultFastPathConfig())

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("   ")}}
	if hit := d.fastPathClassify(req); hit.Hit {
		t.Fatalf("expected miss for whitespace input, got %+v", hit)
	}
}

func TestFastPathClassify_DeclinesWhenAgentMissing(t *testing.T) {
	// Build a dispatcher whose subAgents map has no "coder" entry.
	reg := newTestRegistry()
	chat := &stubAgent{id: "chat"}

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil, nil, nil,
		WithLLM(&countingChatCompleter{}, "test"),
		WithFallbackAgent(chat),
		WithFastPath(DefaultFastPathConfig()),
	)

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("calc 2+2")}}
	hit := d.fastPathClassify(req)

	if hit.Hit {
		t.Fatalf("expected miss when coder missing, got %+v", hit)
	}
}

func TestDispatcher_Run_FastPathShortCircuits(t *testing.T) {
	d, chat, _, llm := newFastPathDispatcher(t, DefaultFastPathConfig())

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("hello")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if llm.calls != 0 {
		t.Errorf("intent LLM calls = %d, want 0", llm.calls)
	}

	if len(resp.Messages) == 0 || resp.Messages[0].Content.Text() != "hi there" {
		t.Errorf("expected chat response, got %+v", resp.Messages)
	}

	_ = chat
}

func TestDispatcher_Run_FastPathDisabledInvokesIntentLLM(t *testing.T) {
	// With fast-path disabled, the request must go through recognizeIntent,
	// which triggers at least one ChatCompletion call on the LLM.
	d, _, _, llm := newFastPathDispatcher(t, DisabledFastPathConfig())

	llm.response = &aimodel.ChatResponse{
		Choices: []aimodel.Choice{{
			Message: aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(`{"mode":"direct","agent":"chat"}`),
			},
		}},
	}

	if _, err := d.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("hello")},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if llm.calls == 0 {
		t.Error("expected intent LLM to be called when fast-path disabled, got 0 calls")
	}
}

func TestDispatcher_RunStream_FastPathEmitsPhaseEvents(t *testing.T) {
	d, _, _, _ := newFastPathDispatcher(t, DefaultFastPathConfig())

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "s1",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	var gotFastStart, gotFastEnd, sawIntent bool
	var endData schema.PhaseEndData

	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				break
			}

			t.Fatalf("Recv: %v", recvErr)
		}

		switch ev.Type {
		case schema.EventPhaseStart:
			if data, ok := ev.Data.(schema.PhaseStartData); ok {
				if data.Phase == fastPathPhase {
					gotFastStart = true
				}
				if data.Phase == "intent" {
					sawIntent = true
				}
			}
		case schema.EventPhaseEnd:
			if data, ok := ev.Data.(schema.PhaseEndData); ok && data.Phase == fastPathPhase {
				gotFastEnd = true
				endData = data
			}
		}
	}

	if !gotFastStart {
		t.Error("expected phase_start with Phase=fast_path")
	}

	if !gotFastEnd {
		t.Error("expected phase_end with Phase=fast_path")
	}

	if sawIntent {
		t.Error("did not expect intent phase when fast-path fired")
	}

	if endData.ToolCalls != 0 || endData.PromptTokens != 0 || endData.CompletionTokens != 0 {
		t.Errorf("fast-path phase_end should show zero costs, got %+v", endData)
	}
}

func TestDefaultFastPathConfig_CompilesAllPatterns(t *testing.T) {
	cfg := DefaultFastPathConfig()

	if !cfg.Enabled {
		t.Fatal("default config should be enabled")
	}

	if cfg.MaxChars != DefaultFastPathMaxChars {
		t.Errorf("MaxChars = %d, want %d", cfg.MaxChars, DefaultFastPathMaxChars)
	}

	for _, r := range cfg.Rules {
		if r.Pattern == nil {
			t.Errorf("rule %+v has nil pattern", r)
		}

		if _, err := regexp.Compile(r.Pattern.String()); err != nil {
			t.Errorf("rule %q failed to compile: %v", r.Pattern.String(), err)
		}
	}
}
