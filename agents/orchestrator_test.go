package agents

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
)

func TestOrchestratorAgent_ImplementsInterfaces(t *testing.T) {
	var _ agent.Agent = (*OrchestratorAgent)(nil)
	var _ agent.StreamAgent = (*OrchestratorAgent)(nil)
}

func TestClassifyResult_Validate_Direct(t *testing.T) {
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
			err := tt.result.validate(subAgents)
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOrchestratorAgent_Run_DirectDispatch(t *testing.T) {
	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"mode": "direct", "agent": "coder"}`),
					},
				},
			},
			Usage: aimodel.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
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

	// Use planner stub that returns classification JSON.
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

	orch := NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		nil, // planGen not needed for direct dispatch
		2,
		chatStub,
		"/tmp/test",
		nil, // no explorer
		plannerStub,
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
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

func TestOrchestratorAgent_Run_FallbackOnClassificationFailure(t *testing.T) {
	mock := &mockChatCompleter{
		err: errors.New("LLM error"),
	}

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

	// Planner that fails.
	plannerStub := &stubAgent{
		id:  "planner",
		err: errors.New("planner error"),
	}

	orch := NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      &stubAgent{id: "coder"},
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		nil,
		2,
		chatStub,
		"",
		nil, // no explorer
		plannerStub,
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
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

func TestOrchestratorAgent_Run_FallbackOnInvalidJSON(t *testing.T) {
	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("not json at all"),
					},
				},
			},
		},
	}

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

	// Planner returns invalid JSON.
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

	orch := NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      &stubAgent{id: "coder"},
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		nil,
		2,
		chatStub,
		"",
		nil, // no explorer
		plannerStub,
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
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

func TestOrchestratorAgent_Run_PlanMode(t *testing.T) {
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

	orch := NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": researcherStub,
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       &stubAgent{id: "chat"},
		},
		planGen,
		2,
		&stubAgent{id: "chat"},
		"/tmp/project",
		nil, // no explorer
		plannerStub,
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
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

func TestOrchestratorAgent_RunStream_DirectDispatch(t *testing.T) {
	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"mode": "direct", "agent": "coder"}`),
					},
				},
			},
		},
	}

	coderStream := &stubStreamAgentForOrch{
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

	orch := NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStream,
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       &stubAgent{id: "chat"},
		},
		nil,
		2,
		&stubAgent{id: "chat"},
		"",
		nil, // no explorer
		plannerStub,
	)

	stream, err := orch.RunStream(context.Background(), &schema.RunRequest{
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

func TestOrchestratorAgent_EnrichRequest(t *testing.T) {
	orch := &OrchestratorAgent{workingDir: "/home/user/project"}

	req := &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "test",
	}

	enriched := orch.enrichRequest(req, "")

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

func TestOrchestratorAgent_EnrichRequest_NoWorkingDir(t *testing.T) {
	orch := &OrchestratorAgent{workingDir: ""}

	req := &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("hello")},
	}

	enriched := orch.enrichRequest(req, "")

	if len(enriched.Messages) != 1 {
		t.Fatalf("enriched messages = %d, want 1 (no enrichment)", len(enriched.Messages))
	}
}

func TestOrchestratorAgent_EnrichRequest_WithContext(t *testing.T) {
	orch := &OrchestratorAgent{workingDir: "/tmp/project"}

	req := &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "test",
	}

	enriched := orch.enrichRequest(req, "Found main.go and orchestrator.go")

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

func TestSchemaToAIModelMessages(t *testing.T) {
	msgs := []schema.Message{
		schema.NewUserMessage("hello"),
		schema.NewAssistantMessage(aimodel.Message{
			Role:    aimodel.RoleAssistant,
			Content: aimodel.NewTextContent("world"),
		}, "test"),
	}

	converted := schema.ToAIModelMessages(msgs)

	if len(converted) != 2 {
		t.Fatalf("converted len = %d, want 2", len(converted))
	}

	if converted[0].Content.Text() != "hello" {
		t.Errorf("converted[0] = %q, want %q", converted[0].Content.Text(), "hello")
	}

	if converted[1].Content.Text() != "world" {
		t.Errorf("converted[1] = %q, want %q", converted[1].Content.Text(), "world")
	}
}

func TestOrchestratorAgent_Run_ChatInPlanSteps(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {"goal": "Explain and code", "steps": [{"id": "step_1", "description": "Explain the concept", "agent": "chat", "depends_on": []}, {"id": "step_2", "description": "Write the code", "agent": "coder", "depends_on": ["step_1"]}]}}`

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
		},
	}

	chatStub := &stubAgent{id: "chat", response: &schema.RunResponse{
		Messages: []schema.Message{schema.NewAssistantMessage(aimodel.Message{
			Role:    aimodel.RoleAssistant,
			Content: aimodel.NewTextContent("explanation done"),
		}, "chat")},
	}}
	coderStub := &stubAgent{id: "coder", response: &schema.RunResponse{
		Messages: []schema.Message{schema.NewAssistantMessage(aimodel.Message{
			Role:    aimodel.RoleAssistant,
			Content: aimodel.NewTextContent("code done"),
		}, "coder")},
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

	orch := NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"",
		nil, // no explorer
		plannerStub,
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("explain and code")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from plan with chat step")
	}
}

// stubStreamAgentForOrch implements agent.StreamAgent for testing orchestrator streaming.
type stubStreamAgentForOrch struct {
	id       string
	response string
}

var _ agent.StreamAgent = (*stubStreamAgentForOrch)(nil)

func (s *stubStreamAgentForOrch) ID() string          { return s.id }
func (s *stubStreamAgentForOrch) Name() string        { return s.id }
func (s *stubStreamAgentForOrch) Description() string { return s.id }

func (s *stubStreamAgentForOrch) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(s.response),
			}, s.id),
		},
	}, nil
}

func (s *stubStreamAgentForOrch) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
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

func TestPlannerSystemPrompt_NotEmpty(t *testing.T) {
	if PlannerSystemPrompt == "" {
		t.Fatal("PlannerSystemPrompt is empty")
	}
}

func TestPlannerSystemPrompt_ContainsJSONSchema(t *testing.T) {
	if !strings.Contains(PlannerSystemPrompt, "direct") {
		t.Error("PlannerSystemPrompt does not contain 'direct'")
	}
	if !strings.Contains(PlannerSystemPrompt, "plan") {
		t.Error("PlannerSystemPrompt does not contain 'plan'")
	}
	if !strings.Contains(PlannerSystemPrompt, "depends_on") {
		t.Error("PlannerSystemPrompt does not contain 'depends_on'")
	}
}

func TestExplorerSystemPrompt_NotEmpty(t *testing.T) {
	if ExplorerSystemPrompt == "" {
		t.Fatal("ExplorerSystemPrompt is empty")
	}
}

func TestPlanSummaryPrompt_NotEmpty(t *testing.T) {
	if PlanSummaryPrompt == "" {
		t.Fatal("PlanSummaryPrompt is empty")
	}
}

func TestOrchestratorAgent_Explore_NilExplorer(t *testing.T) {
	orch := &OrchestratorAgent{}

	summary, usage := orch.explore(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("test")},
	})
	if summary != "" {
		t.Errorf("expected empty summary, got %q", summary)
	}
	if usage != nil {
		t.Error("expected nil usage")
	}
}

func TestOrchestratorAgent_Explore_WithExplorer(t *testing.T) {
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

	orch := &OrchestratorAgent{
		workingDir:    "/tmp/project",
		explorerAgent: explorerStub,
	}

	summary, usage := orch.explore(context.Background(), &schema.RunRequest{
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

func TestOrchestratorAgent_PlanTask_WithPlanner(t *testing.T) {
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

	orch := &OrchestratorAgent{
		workingDir:   "/tmp/project",
		plannerAgent: plannerStub,
		subAgents: map[string]agent.Agent{
			"coder": &stubAgent{id: "coder"},
			"chat":  &stubAgent{id: "chat"},
		},
	}

	result, usage, err := orch.planTask(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("test")},
	}, "Some context")
	if err != nil {
		t.Fatalf("planTask: %v", err)
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

func TestOrchestratorAgent_PlanTask_FallbackToDirect(t *testing.T) {
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

	orch := &OrchestratorAgent{
		llm:          mock,
		model:        "test-model",
		plannerAgent: nil, // no planner, falls back to direct LLM call
		subAgents: map[string]agent.Agent{
			"coder": &stubAgent{id: "coder"},
			"chat":  &stubAgent{id: "chat"},
		},
	}

	result, _, err := orch.planTask(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("hello")},
	}, "")
	if err != nil {
		t.Fatalf("planTask: %v", err)
	}
	if result.Mode != "direct" {
		t.Errorf("mode = %q, want %q", result.Mode, "direct")
	}
	if result.Agent != "chat" {
		t.Errorf("agent = %q, want %q", result.Agent, "chat")
	}
}

func TestOrchestratorAgent_Run_WithExplorerAndPlanner(t *testing.T) {
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

	orch := NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator"},
		nil, // llm not needed when both explorer and planner are stubs
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		nil,
		2,
		chatStub,
		"/tmp/project",
		explorerStub,
		plannerStub,
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
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
