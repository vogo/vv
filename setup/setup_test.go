package setup

import (
	"context"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/configs"
)

// mockChatCompleter is a simple mock for testing.
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

func TestNew_AllAgentsCreated(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if result.Dispatcher == nil {
		t.Fatal("expected non-nil Dispatcher")
	}

	if result.Dispatcher.ID() != "orchestrator" {
		t.Errorf("Dispatcher ID = %q, want %q", result.Dispatcher.ID(), "orchestrator")
	}

	// Verify all dispatchable agents.
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		a := result.Agent(id)
		if a == nil {
			t.Errorf("expected agent %q to be created", id)
		} else if a.ID() != id {
			t.Errorf("agent ID = %q, want %q", a.ID(), id)
		}
	}
}

func TestNew_AgentNames(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	expected := map[string]string{
		"coder":      "Coder Agent",
		"chat":       "Chat Agent",
		"researcher": "Researcher Agent",
		"reviewer":   "Reviewer Agent",
	}

	for id, wantName := range expected {
		a := result.Agent(id)
		if a == nil {
			t.Errorf("expected agent %q", id)

			continue
		}

		if a.Name() != wantName {
			t.Errorf("%s.Name() = %q, want %q", id, a.Name(), wantName)
		}
	}
}

func TestNew_AgentsReturnsAllDispatchable(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	agents := result.Agents()
	if len(agents) != 4 {
		t.Errorf("Agents() = %d, want 4 (coder, researcher, reviewer, chat)", len(agents))
	}
}

func TestNew_DispatcherName(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if result.Dispatcher.Name() != "Orchestrator Agent" {
		t.Errorf("Dispatcher Name = %q, want %q", result.Dispatcher.Name(), "Orchestrator Agent")
	}
}

func TestNew_WithWrapToolRegistry(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	wrapCalled := false
	result, err := New(cfg, mock, nil, nil, &Options{
		WrapToolRegistry: func(r *tool.Registry) tool.ToolRegistry {
			wrapCalled = true

			return r
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if result.Dispatcher == nil {
		t.Fatal("expected non-nil Dispatcher")
	}

	if !wrapCalled {
		t.Error("expected WrapToolRegistry to be called")
	}
}

func TestNew_AgentNotFound(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	a := result.Agent("nonexistent")
	if a != nil {
		t.Errorf("expected nil for nonexistent agent, got %v", a)
	}
}
