package dispatch

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/orchestrate"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vagents/vaga/registry"
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

func newTestRegistry() *registry.Registry {
	reg := registry.New()
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		reg.MustRegister(registry.AgentDescriptor{
			ID:           id,
			DisplayName:  id,
			Description:  id,
			Dispatchable: true,
		})
	}

	return reg
}

func TestDispatcher_ImplementsInterfaces(t *testing.T) {
	var _ agent.Agent = (*Dispatcher)(nil)
	var _ agent.StreamAgent = (*Dispatcher)(nil)
}

func TestClassifyResult_Validate_Direct(t *testing.T) {
	reg := newTestRegistry()
	subAgents := map[string]agent.Agent{
		"coder":      &stubAgent{id: "coder"},
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	tests := []struct {
		name    string
		result  ClassifyResult
		wantErr bool
	}{
		{
			name:    "valid direct coder",
			result:  ClassifyResult{Mode: "direct", Agent: "coder"},
			wantErr: false,
		},
		{
			name:    "valid direct chat",
			result:  ClassifyResult{Mode: "direct", Agent: "chat"},
			wantErr: false,
		},
		{
			name:    "invalid direct unknown agent",
			result:  ClassifyResult{Mode: "direct", Agent: "nonexistent"},
			wantErr: true,
		},
		{
			name: "valid plan",
			result: ClassifyResult{
				Mode: "plan",
				Plan: &Plan{
					Goal: "test",
					Steps: []PlanStep{
						{ID: "step_1", Description: "do it", Agent: "coder", DependsOn: []string{}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "plan with invalid agent",
			result: ClassifyResult{
				Mode: "plan",
				Plan: &Plan{
					Goal: "test",
					Steps: []PlanStep{
						{ID: "step_1", Description: "do it", Agent: "nonexistent", DependsOn: []string{}},
					},
				},
			},
			wantErr: true,
		},
		{
			name:    "plan with nil plan",
			result:  ClassifyResult{Mode: "plan"},
			wantErr: true,
		},
		{
			name: "plan with empty steps",
			result: ClassifyResult{
				Mode: "plan",
				Plan: &Plan{Goal: "test", Steps: []PlanStep{}},
			},
			wantErr: true,
		},
		{
			name:    "unknown mode",
			result:  ClassifyResult{Mode: "unknown"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.result.validate(reg, subAgents)
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDispatcher_Run_DirectDispatch(t *testing.T) {
	reg := newTestRegistry()

	coderStub := &stubAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("coder response"),
				}, "coder"),
			},
			Usage: &aimodel.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
		},
	}

	chatStub := &stubAgent{id: "chat"}

	plannerStub := &stubAgent{
		id: "planner",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent(`{"mode": "direct", "agent": "coder"}`),
				}, "planner"),
			},
			Usage: &aimodel.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}

	subAgents := map[string]agent.Agent{
		"coder":      coderStub,
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       chatStub,
	}

	d := New(
		reg,
		subAgents,
		nil, // no explorer
		plannerStub,
		nil, // planGen not needed for direct dispatch
		WithLLM(&mockChatCompleter{}, "test-model"),
		WithMaxConcurrency(2),
		WithFallbackAgent(chatStub),
		WithWorkingDir("/tmp/test"),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("write some code")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected response messages")
	}

	text := resp.Messages[0].Content.Text()
	if text != "coder response" {
		t.Errorf("response = %q, want %q", text, "coder response")
	}

	// Verify usage aggregation.
	if resp.Usage == nil {
		t.Fatal("expected usage in response")
	}

	if resp.Usage.PromptTokens != 110 {
		t.Errorf("PromptTokens = %d, want 110", resp.Usage.PromptTokens)
	}

	if resp.Usage.TotalTokens != 165 {
		t.Errorf("TotalTokens = %d, want 165", resp.Usage.TotalTokens)
	}
}

func TestDispatcher_Run_FallbackOnClassificationFailure(t *testing.T) {
	reg := newTestRegistry()

	chatStub := &stubAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("fallback response"),
				}, "chat"),
			},
		},
	}

	plannerStub := &stubAgent{
		id:  "planner",
		err: errors.New("planner error"),
	}

	subAgents := map[string]agent.Agent{
		"coder":      &stubAgent{id: "coder"},
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       chatStub,
	}

	d := New(
		reg,
		subAgents,
		nil,
		plannerStub,
		nil,
		WithLLM(&mockChatCompleter{err: errors.New("LLM error")}, "test-model"),
		WithFallbackAgent(chatStub),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("test")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected fallback response messages")
	}

	text := resp.Messages[0].Content.Text()
	if text != "fallback response" {
		t.Errorf("response = %q, want %q", text, "fallback response")
	}
}

func TestDispatcher_Run_FallbackOnInvalidJSON(t *testing.T) {
	reg := newTestRegistry()

	chatStub := &stubAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("chat fallback"),
				}, "chat"),
			},
		},
	}

	plannerStub := &stubAgent{
		id: "planner",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("not json at all"),
				}, "planner"),
			},
		},
	}

	subAgents := map[string]agent.Agent{
		"coder":      &stubAgent{id: "coder"},
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       chatStub,
	}

	d := New(
		reg,
		subAgents,
		nil,
		plannerStub,
		nil,
		WithLLM(&mockChatCompleter{}, "test-model"),
		WithFallbackAgent(chatStub),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("test")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	text := resp.Messages[0].Content.Text()
	if text != "chat fallback" {
		t.Errorf("response = %q, want %q", text, "chat fallback")
	}
}

func TestDispatcher_Run_PlanMode(t *testing.T) {
	reg := newTestRegistry()
	planJSON := `{"mode": "plan", "plan": {"goal": "Test project", "steps": [{"id": "step_1", "description": "Research", "agent": "researcher", "depends_on": []}, {"id": "step_2", "description": "Code it", "agent": "coder", "depends_on": ["step_1"]}]}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				},
			},
			Usage: aimodel.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}

	coderStub := &stubAgent{id: "coder", response: &schema.RunResponse{
		Messages: []schema.Message{schema.NewAssistantMessage(aimodel.Message{
			Role:    aimodel.RoleAssistant,
			Content: aimodel.NewTextContent("code done"),
		}, "coder")},
	}}
	researcherStub := &stubAgent{id: "researcher", response: &schema.RunResponse{
		Messages: []schema.Message{schema.NewAssistantMessage(aimodel.Message{
			Role:    aimodel.RoleAssistant,
			Content: aimodel.NewTextContent("research done"),
		}, "researcher")},
	}}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	plannerStub := &stubAgent{
		id: "planner",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent(planJSON),
				}, "planner"),
			},
		},
	}

	subAgents := map[string]agent.Agent{
		"coder":      coderStub,
		"researcher": researcherStub,
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	d := New(
		reg,
		subAgents,
		nil, // no explorer
		plannerStub,
		planGen,
		WithLLM(mock, "test-model"),
		WithMaxConcurrency(2),
		WithFallbackAgent(&stubAgent{id: "chat"}),
		WithWorkingDir("/tmp/project"),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("Set up a Go project")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
}

func TestDispatcher_RunStream_DirectDispatch(t *testing.T) {
	reg := newTestRegistry()

	coderStream := &stubStreamAgent{
		id:       "coder",
		response: "streamed coder response",
	}

	plannerStub := &stubAgent{
		id: "planner",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent(`{"mode": "direct", "agent": "coder"}`),
				}, "planner"),
			},
		},
	}

	subAgents := map[string]agent.Agent{
		"coder":      coderStream,
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	d := New(
		reg,
		subAgents,
		nil, // no explorer
		plannerStub,
		nil,
		WithLLM(&mockChatCompleter{}, "test-model"),
		WithMaxConcurrency(2),
		WithFallbackAgent(&stubAgent{id: "chat"}),
	)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("test")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	var gotStart, gotEnd bool

	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				break
			}

			t.Fatalf("Recv: %v", recvErr)
		}

		switch event.Type {
		case schema.EventAgentStart:
			gotStart = true
		case schema.EventAgentEnd:
			gotEnd = true
		}
	}

	if !gotStart {
		t.Error("expected AgentStart event")
	}

	if !gotEnd {
		t.Error("expected AgentEnd event")
	}
}

func TestDispatcher_EnrichRequest(t *testing.T) {
	d := &Dispatcher{workingDir: "/home/user/project"}

	req := &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "test",
	}

	enriched := d.enrichRequest(req, "")

	if len(enriched.Messages) != 2 {
		t.Fatalf("enriched messages = %d, want 2", len(enriched.Messages))
	}

	if enriched.Messages[0].Content.Text() != "Working directory: /home/user/project" {
		t.Errorf("first message = %q, want working directory context", enriched.Messages[0].Content.Text())
	}

	if enriched.SessionID != "test" {
		t.Errorf("SessionID = %q, want %q", enriched.SessionID, "test")
	}
}

func TestDispatcher_EnrichRequest_NoWorkingDir(t *testing.T) {
	d := &Dispatcher{workingDir: ""}

	req := &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("hello")},
	}

	enriched := d.enrichRequest(req, "")

	if len(enriched.Messages) != 1 {
		t.Fatalf("enriched messages = %d, want 1 (no enrichment)", len(enriched.Messages))
	}
}

func TestDispatcher_EnrichRequest_WithContext(t *testing.T) {
	d := &Dispatcher{workingDir: "/tmp/project"}

	req := &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "test",
	}

	enriched := d.enrichRequest(req, "Found main.go and orchestrator.go")

	if len(enriched.Messages) != 3 {
		t.Fatalf("enriched messages = %d, want 3", len(enriched.Messages))
	}

	if enriched.Messages[0].Content.Text() != "Working directory: /tmp/project" {
		t.Errorf("first message = %q, want working dir", enriched.Messages[0].Content.Text())
	}

	if enriched.Messages[1].Content.Text() != "Project context:\nFound main.go and orchestrator.go" {
		t.Errorf("second message = %q, want project context", enriched.Messages[1].Content.Text())
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain json",
			input: `{"goal": "test"}`,
			want:  `{"goal": "test"}`,
		},
		{
			name:  "json code fence",
			input: "```json\n{\"goal\": \"test\"}\n```",
			want:  `{"goal": "test"}`,
		},
		{
			name:  "plain code fence",
			input: "```\n{\"goal\": \"test\"}\n```",
			want:  `{"goal": "test"}`,
		},
		{
			name:  "json with surrounding text",
			input: "Here is the plan: {\"goal\": \"test\"} done.",
			want:  `{"goal": "test"}`,
		},
		{
			name:  "json with braces inside string values",
			input: `{"goal": "write func() { return }"}`,
			want:  `{"goal": "write func() { return }"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSON(tt.input)
			if got != tt.want {
				t.Errorf("extractJSON = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindTerminalNodes(t *testing.T) {
	nodes := []struct {
		id   string
		deps []string
	}{
		{"a", nil},
		{"b", []string{"a"}},
		{"c", []string{"a"}},
		{"d", []string{"b"}},
	}

	var orchNodes []orchestrate.Node
	for _, n := range nodes {
		orchNodes = append(orchNodes, orchestrate.Node{ID: n.id, Deps: n.deps})
	}

	terminals := findTerminalNodes(orchNodes)

	if len(terminals) != 2 {
		t.Fatalf("terminal nodes = %d, want 2", len(terminals))
	}

	termMap := make(map[string]bool)
	for _, id := range terminals {
		termMap[id] = true
	}

	if !termMap["c"] {
		t.Error("expected c to be terminal")
	}

	if !termMap["d"] {
		t.Error("expected d to be terminal")
	}
}

func TestPlanAggregator_SingleResult(t *testing.T) {
	agg := &PlanAggregator{}

	results := map[string]*schema.RunResponse{
		"step_1": {
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("single result"),
				}, "coder"),
			},
		},
	}

	resp, err := agg.Aggregate(context.Background(), results)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected messages")
	}

	if resp.Messages[0].Content.Text() != "single result" {
		t.Errorf("text = %q, want %q", resp.Messages[0].Content.Text(), "single result")
	}
}

func TestPlanAggregator_EmptyResults(t *testing.T) {
	agg := &PlanAggregator{}

	resp, err := agg.Aggregate(context.Background(), map[string]*schema.RunResponse{})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	if len(resp.Messages) != 0 {
		t.Errorf("expected no messages, got %d", len(resp.Messages))
	}
}

func TestAggregateUsage(t *testing.T) {
	tests := []struct {
		name    string
		a       *aimodel.Usage
		b       *aimodel.Usage
		wantNil bool
		wantPT  int
	}{
		{
			name:    "both nil",
			wantNil: true,
		},
		{
			name:   "a only",
			a:      &aimodel.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			wantPT: 10,
		},
		{
			name:   "b only",
			b:      &aimodel.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30},
			wantPT: 20,
		},
		{
			name:   "both present",
			a:      &aimodel.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			b:      &aimodel.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30},
			wantPT: 30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := aggregateUsage(tt.a, tt.b)
			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}

				return
			}

			if result == nil {
				t.Fatal("expected non-nil usage")
			}

			if result.PromptTokens != tt.wantPT {
				t.Errorf("PromptTokens = %d, want %d", result.PromptTokens, tt.wantPT)
			}
		})
	}
}

func TestDispatcher_Explore_NilExplorer(t *testing.T) {
	d := &Dispatcher{}

	summary, usage := d.explore(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("test")},
	})

	if summary != "" {
		t.Errorf("expected empty summary, got %q", summary)
	}

	if usage != nil {
		t.Error("expected nil usage")
	}
}

func TestDispatcher_Explore_WithExplorer(t *testing.T) {
	explorerStub := &stubAgent{
		id: "explorer",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Found main.go and orchestrator.go"),
				}, "explorer"),
			},
			Usage: &aimodel.Usage{PromptTokens: 50, CompletionTokens: 20, TotalTokens: 70},
		},
	}

	d := &Dispatcher{
		workingDir:    "/tmp/project",
		explorerAgent: explorerStub,
	}

	summary, usage := d.explore(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("how does the orchestrator work?")},
	})

	if summary != "Found main.go and orchestrator.go" {
		t.Errorf("summary = %q, want explorer output", summary)
	}

	if usage == nil {
		t.Fatal("expected non-nil usage")
	}

	if usage.TotalTokens != 70 {
		t.Errorf("TotalTokens = %d, want 70", usage.TotalTokens)
	}
}

func TestDispatcher_Classify_WithPlanner(t *testing.T) {
	reg := newTestRegistry()

	plannerStub := &stubAgent{
		id: "planner",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent(`{"mode": "direct", "agent": "coder"}`),
				}, "planner"),
			},
			Usage: &aimodel.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}

	d := &Dispatcher{
		workingDir:   "/tmp/project",
		plannerAgent: plannerStub,
		registry:     reg,
		subAgents: map[string]agent.Agent{
			"coder": &stubAgent{id: "coder"},
			"chat":  &stubAgent{id: "chat"},
		},
	}

	result, usage, err := d.classify(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("test")},
	}, "Some context")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}

	if result.Mode != "direct" {
		t.Errorf("mode = %q, want %q", result.Mode, "direct")
	}

	if result.Agent != "coder" {
		t.Errorf("agent = %q, want %q", result.Agent, "coder")
	}

	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
}

func TestDispatcher_Classify_FallbackToDirect(t *testing.T) {
	reg := newTestRegistry()

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"mode": "direct", "agent": "chat"}`),
					},
				},
			},
			Usage: aimodel.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}

	d := &Dispatcher{
		llm:          mock,
		model:        "test-model",
		plannerAgent: nil, // no planner, falls back to direct LLM call
		registry:     reg,
		subAgents: map[string]agent.Agent{
			"coder": &stubAgent{id: "coder"},
			"chat":  &stubAgent{id: "chat"},
		},
	}

	result, _, err := d.classify(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("hello")},
	}, "")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}

	if result.Mode != "direct" {
		t.Errorf("mode = %q, want %q", result.Mode, "direct")
	}

	if result.Agent != "chat" {
		t.Errorf("agent = %q, want %q", result.Agent, "chat")
	}
}

func TestDispatcher_Run_WithExplorerAndPlanner(t *testing.T) {
	reg := newTestRegistry()

	explorerStub := &stubAgent{
		id: "explorer",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Found orchestrator.go and agents.go"),
				}, "explorer"),
			},
			Usage: &aimodel.Usage{PromptTokens: 30, CompletionTokens: 10, TotalTokens: 40},
		},
	}

	plannerStub := &stubAgent{
		id: "planner",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent(`{"mode": "direct", "agent": "coder"}`),
				}, "planner"),
			},
			Usage: &aimodel.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}

	coderStub := &stubAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("coder response"),
				}, "coder"),
			},
			Usage: &aimodel.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
		},
	}

	chatStub := &stubAgent{id: "chat"}

	subAgents := map[string]agent.Agent{
		"coder":      coderStub,
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       chatStub,
	}

	d := New(
		reg,
		subAgents,
		explorerStub,
		plannerStub,
		nil,
		WithLLM(nil, "test-model"),
		WithMaxConcurrency(2),
		WithFallbackAgent(chatStub),
		WithWorkingDir("/tmp/project"),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("how does orchestrator work?")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected response messages")
	}

	text := resp.Messages[0].Content.Text()
	if text != "coder response" {
		t.Errorf("response = %q, want %q", text, "coder response")
	}
}

func TestDynamicAgentSpec_Validate(t *testing.T) {
	reg := newTestRegistry()

	tests := []struct {
		name    string
		spec    DynamicAgentSpec
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid coder",
			spec:    DynamicAgentSpec{BaseType: "coder"},
			wantErr: false,
		},
		{
			name:    "valid researcher",
			spec:    DynamicAgentSpec{BaseType: "researcher"},
			wantErr: false,
		},
		{
			name:    "valid reviewer",
			spec:    DynamicAgentSpec{BaseType: "reviewer"},
			wantErr: false,
		},
		{
			name:    "valid chat",
			spec:    DynamicAgentSpec{BaseType: "chat"},
			wantErr: false,
		},
		{
			name:    "valid with all fields",
			spec:    DynamicAgentSpec{BaseType: "coder", SystemPrompt: "custom", ToolAccess: "read-only", Model: "gpt-4"},
			wantErr: false,
		},
		{
			name:    "valid tool_access full",
			spec:    DynamicAgentSpec{BaseType: "coder", ToolAccess: "full"},
			wantErr: false,
		},
		{
			name:    "valid tool_access none",
			spec:    DynamicAgentSpec{BaseType: "coder", ToolAccess: "none"},
			wantErr: false,
		},
		{
			name:    "missing base_type",
			spec:    DynamicAgentSpec{},
			wantErr: true,
			errMsg:  "base_type is required",
		},
		{
			name:    "invalid base_type",
			spec:    DynamicAgentSpec{BaseType: "unknown"},
			wantErr: true,
			errMsg:  "invalid base_type",
		},
		{
			name:    "invalid tool_access",
			spec:    DynamicAgentSpec{BaseType: "coder", ToolAccess: "write-only"},
			wantErr: true,
			errMsg:  "invalid tool_access",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.validate(reg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr && err != nil && tt.errMsg != "" {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("validate() error = %q, want to contain %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestPlanSummaryPrompt_NotEmpty(t *testing.T) {
	if PlanSummaryPrompt == "" {
		t.Fatal("PlanSummaryPrompt is empty")
	}
}

// stubStreamAgent implements agent.StreamAgent for testing.
type stubStreamAgent struct {
	id       string
	response string
}

var _ agent.StreamAgent = (*stubStreamAgent)(nil)

func (s *stubStreamAgent) ID() string          { return s.id }
func (s *stubStreamAgent) Name() string        { return s.id }
func (s *stubStreamAgent) Description() string { return s.id }

func (s *stubStreamAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(s.response),
			}, s.id),
		},
	}, nil
}

func (s *stubStreamAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, 8, func(ctx context.Context, send func(schema.Event) error) error {
		if err := send(schema.NewEvent(schema.EventAgentStart, s.id, req.SessionID, schema.AgentStartData{})); err != nil {
			return err
		}

		if err := send(schema.NewEvent(schema.EventTextDelta, s.id, req.SessionID, schema.TextDeltaData{Delta: s.response})); err != nil {
			return err
		}

		return send(schema.NewEvent(schema.EventAgentEnd, s.id, req.SessionID, schema.AgentEndData{
			Message: s.response,
		}))
	}), nil
}
