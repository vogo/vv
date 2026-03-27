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

func TestCreate_AllAgents(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	reg := newTestRegistry(t)

	allAgents := Create(cfg, mock, reg, reg, reg, nil, nil)

	if allAgents.Coder.ID() != "coder" {
		t.Errorf("coder ID = %q, want %q", allAgents.Coder.ID(), "coder")
	}
	if allAgents.Chat.ID() != "chat" {
		t.Errorf("chat ID = %q, want %q", allAgents.Chat.ID(), "chat")
	}
	if allAgents.Researcher.ID() != "researcher" {
		t.Errorf("researcher ID = %q, want %q", allAgents.Researcher.ID(), "researcher")
	}
	if allAgents.Reviewer.ID() != "reviewer" {
		t.Errorf("reviewer ID = %q, want %q", allAgents.Reviewer.ID(), "reviewer")
	}
	if allAgents.Orchestrator.ID() != "orchestrator" {
		t.Errorf("orchestrator ID = %q, want %q", allAgents.Orchestrator.ID(), "orchestrator")
	}
}

func TestCreate_CoderHasTools(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	reg := newTestRegistry(t)

	allAgents := Create(cfg, mock, reg, reg, reg, nil, nil)
	_ = allAgents.Coder.Tools()
}

func TestCreate_ChatHasNoTools(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	reg := newTestRegistry(t)

	allAgents := Create(cfg, mock, reg, reg, reg, nil, nil)
	tools := allAgents.Chat.Tools()

	if len(tools) != 0 {
		t.Errorf("chat tools = %d, want 0", len(tools))
	}
}

func TestCreate_Names(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	reg := newTestRegistry(t)

	allAgents := Create(cfg, mock, reg, reg, reg, nil, nil)

	if allAgents.Coder.Name() != "Coder Agent" {
		t.Errorf("coder Name = %q, want %q", allAgents.Coder.Name(), "Coder Agent")
	}

	if allAgents.Chat.Name() != "Chat Agent" {
		t.Errorf("chat Name = %q, want %q", allAgents.Chat.Name(), "Chat Agent")
	}

	if allAgents.Researcher.Name() != "Researcher Agent" {
		t.Errorf("researcher Name = %q, want %q", allAgents.Researcher.Name(), "Researcher Agent")
	}

	if allAgents.Reviewer.Name() != "Reviewer Agent" {
		t.Errorf("reviewer Name = %q, want %q", allAgents.Reviewer.Name(), "Reviewer Agent")
	}

	if allAgents.Orchestrator.Name() != "Orchestrator Agent" {
		t.Errorf("orchestrator Name = %q, want %q", allAgents.Orchestrator.Name(), "Orchestrator Agent")
	}
}

// stubAgent is a minimal agent implementation for testing.
type stubAgent struct {
	id       string
	response *schema.RunResponse
	err      error
}

var _ agent.Agent = (*stubAgent)(nil)

func (s *stubAgent) ID() string          { return s.id }
func (s *stubAgent) Name() string        { return s.id }
func (s *stubAgent) Description() string { return s.id }

func (s *stubAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
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
