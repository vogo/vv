package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/registries"
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

func TestRegisterAll_AgentIDs(t *testing.T) {
	reg := registries.New()
	RegisterCoder(reg)
	RegisterResearcher(reg)
	RegisterReviewer(reg)
	RegisterPlanner(reg)

	// Verify all expected agents are registered. Chat and
	// explorer no longer exist — the unified Primary covers chat inline
	// and exploration via its read/glob/grep tools.
	for _, id := range []string{"coder", "researcher", "reviewer", "planner"} {
		if _, ok := reg.Get(id); !ok {
			t.Errorf("expected agent %q to be registered", id)
		}
	}

	for _, id := range []string{"chat", "explorer"} {
		if _, ok := reg.Get(id); ok {
			t.Errorf("agent %q must not be registered", id)
		}
	}
}

func TestRegisterAll_Dispatchable(t *testing.T) {
	reg := registries.New()
	RegisterCoder(reg)
	RegisterResearcher(reg)
	RegisterReviewer(reg)
	RegisterPlanner(reg)

	dispatchable := reg.Dispatchable()
	dispatchableIDs := make(map[string]bool)

	for _, d := range dispatchable {
		dispatchableIDs[d.ID] = true
	}

	// Dispatchable: coder, researcher, reviewer.
	for _, id := range []string{"coder", "researcher", "reviewer"} {
		if !dispatchableIDs[id] {
			t.Errorf("expected %q to be dispatchable", id)
		}
	}

	// Not dispatchable: planner.
	if dispatchableIDs["planner"] {
		t.Errorf("expected planner to NOT be dispatchable")
	}
}

func TestFactory_CoderAgent(t *testing.T) {
	reg := registries.New()
	RegisterCoder(reg)

	desc, ok := reg.Get("coder")
	if !ok {
		t.Fatal("coder not registered")
	}

	mock := &mockChatCompleter{}

	a, err := desc.Factory(registries.FactoryOptions{
		LLM:           mock,
		Model:         "test-model",
		MaxIterations: 10,
	})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}

	if a.ID() != "coder" {
		t.Errorf("ID = %q, want %q", a.ID(), "coder")
	}

	if a.Name() != "Coder Agent" {
		t.Errorf("Name = %q, want %q", a.Name(), "Coder Agent")
	}
}

func TestFactory_ResearcherAgent(t *testing.T) {
	reg := registries.New()
	RegisterResearcher(reg)

	desc, ok := reg.Get("researcher")
	if !ok {
		t.Fatal("researcher not registered")
	}

	mock := &mockChatCompleter{}

	a, err := desc.Factory(registries.FactoryOptions{
		LLM:           mock,
		Model:         "test-model",
		MaxIterations: 10,
	})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}

	if a.ID() != "researcher" {
		t.Errorf("ID = %q, want %q", a.ID(), "researcher")
	}

	if a.Name() != "Researcher Agent" {
		t.Errorf("Name = %q, want %q", a.Name(), "Researcher Agent")
	}
}

func TestFactory_ReviewerAgent(t *testing.T) {
	reg := registries.New()
	RegisterReviewer(reg)

	desc, ok := reg.Get("reviewer")
	if !ok {
		t.Fatal("reviewer not registered")
	}

	mock := &mockChatCompleter{}

	a, err := desc.Factory(registries.FactoryOptions{
		LLM:           mock,
		Model:         "test-model",
		MaxIterations: 10,
	})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}

	if a.ID() != "reviewer" {
		t.Errorf("ID = %q, want %q", a.ID(), "reviewer")
	}

	if a.Name() != "Reviewer Agent" {
		t.Errorf("Name = %q, want %q", a.Name(), "Reviewer Agent")
	}
}

func TestBuildPlannerSystemPrompt(t *testing.T) {
	reg := registries.New()
	RegisterCoder(reg)
	RegisterResearcher(reg)
	RegisterReviewer(reg)

	prompt := BuildPlannerSystemPrompt(reg)

	if !strings.Contains(prompt, "coder") {
		t.Error("planner prompt should contain 'coder'")
	}

	if !strings.Contains(prompt, "researcher") {
		t.Error("planner prompt should contain 'researcher'")
	}

	if !strings.Contains(prompt, "reviewer") {
		t.Error("planner prompt should contain 'reviewer'")
	}

	// Should not contain the template placeholder.
	if strings.Contains(prompt, "{{.AgentList}}") {
		t.Error("planner prompt should not contain the template placeholder")
	}
}
