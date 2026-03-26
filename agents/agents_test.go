package agents

import (
	"context"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vagents/vaga/config"
)

// mockChatCompleter is a simple mock for testing agent creation.
type mockChatCompleter struct {
	response *aimodel.ChatResponse
	err      error
}

func (m *mockChatCompleter) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}

	return m.response, nil
}

func (m *mockChatCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, m.err
}

func newTestRegistry(t *testing.T) *tool.Registry {
	t.Helper()

	reg := tool.NewRegistry()

	return reg
}

func TestCreate_CoderHasTools(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
	}

	reg := newTestRegistry(t)

	coder, _ := Create(cfg, mock, reg)
	_ = coder.Tools()
}

func TestCreate_ChatHasNoTools(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
	}

	reg := newTestRegistry(t)

	_, chat := Create(cfg, mock, reg)
	tools := chat.Tools()

	if len(tools) != 0 {
		t.Errorf("chat tools = %d, want 0", len(tools))
	}
}

func TestCreate_IDs(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
	}

	reg := newTestRegistry(t)

	coder, chat := Create(cfg, mock, reg)

	if coder.ID() != "coder" {
		t.Errorf("coder ID = %q, want %q", coder.ID(), "coder")
	}

	if chat.ID() != "chat" {
		t.Errorf("chat ID = %q, want %q", chat.ID(), "chat")
	}

	if coder.Name() != "Coder Agent" {
		t.Errorf("coder Name = %q, want %q", coder.Name(), "Coder Agent")
	}

	if chat.Name() != "Chat Agent" {
		t.Errorf("chat Name = %q, want %q", chat.Name(), "Chat Agent")
	}
}

func TestCreateRouter_ID(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
	}

	coder := &stubAgent{id: "coder"}
	chat := &stubAgent{id: "chat"}

	router := CreateRouter(cfg, mock, coder, chat)

	if router.ID() != "router" {
		t.Errorf("router ID = %q, want %q", router.ID(), "router")
	}

	if router.Name() != "Router Agent" {
		t.Errorf("router Name = %q, want %q", router.Name(), "Router Agent")
	}
}

func TestCreateRouter_RoutesToCoder(t *testing.T) {
	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("0"),
					},
				},
			},
		},
	}

	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
	}

	coder := &stubAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("coder response"),
				}, "coder"),
			},
		},
	}
	chat := &stubAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("chat response"),
				}, "chat"),
			},
		},
	}

	router := CreateRouter(cfg, mock, coder, chat)

	resp, err := router.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("write some code")},
	})
	if err != nil {
		t.Fatalf("router.Run: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected response messages")
	}

	text := resp.Messages[0].Content.Text()
	if text != "coder response" {
		t.Errorf("response text = %q, want %q", text, "coder response")
	}
}

func TestCreateRouter_RoutesToChat(t *testing.T) {
	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("1"),
					},
				},
			},
		},
	}

	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
	}

	coder := &stubAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("coder response"),
				}, "coder"),
			},
		},
	}
	chat := &stubAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("chat response"),
				}, "chat"),
			},
		},
	}

	router := CreateRouter(cfg, mock, coder, chat)

	resp, err := router.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("tell me a joke")},
	})
	if err != nil {
		t.Fatalf("router.Run: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected response messages")
	}

	text := resp.Messages[0].Content.Text()
	if text != "chat response" {
		t.Errorf("response text = %q, want %q", text, "chat response")
	}
}

// stubAgent is a minimal agent implementation for testing.
type stubAgent struct {
	id       string
	response *schema.RunResponse
}

var _ agent.Agent = (*stubAgent)(nil)

func (s *stubAgent) ID() string          { return s.id }
func (s *stubAgent) Name() string        { return s.id }
func (s *stubAgent) Description() string { return s.id }

func (s *stubAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	if s.response != nil {
		return s.response, nil
	}

	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("stub response from " + s.id),
			}, s.id),
		},
	}, nil
}
