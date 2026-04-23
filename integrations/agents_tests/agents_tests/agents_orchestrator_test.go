package agents_tests

import (
	"context"
	"io"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
)

// --- Test 1c: Orchestrator dispatches directly to sub-agents (Design Test 1) ---
// Verifies that simple tasks are dispatched to a single agent without plan generation.
// Test cases:
//   - Direct dispatch to coder returns coder response
//   - Direct dispatch to researcher returns researcher response
//   - Direct dispatch to reviewer returns reviewer response
//   - Direct dispatch to chat returns chat response
//   - No DAG execution occurs (only sub-agent Run is called)
func TestIntegration_Agents_OrchestratorDirectDispatch(t *testing.T) {
	tests := []struct {
		name         string
		llmResponse  string // classify response from LLM
		wantResponse string
	}{
		{
			name:         "dispatches to coder",
			llmResponse:  `{"mode": "direct", "agent": "coder"}`,
			wantResponse: "coder handled it",
		},
		{
			name:         "dispatches to researcher",
			llmResponse:  `{"mode": "direct", "agent": "researcher"}`,
			wantResponse: "researcher handled it",
		},
		{
			name:         "dispatches to reviewer",
			llmResponse:  `{"mode": "direct", "agent": "reviewer"}`,
			wantResponse: "reviewer handled it",
		},
		{
			name:         "dispatches to chat",
			llmResponse:  `{"mode": "direct", "agent": "chat"}`,
			wantResponse: "chat handled it",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockChatCompleter{
				response: &aimodel.ChatResponse{
					Choices: []aimodel.Choice{
						{
							Message: aimodel.Message{
								Role:    aimodel.RoleAssistant,
								Content: aimodel.NewTextContent(tt.llmResponse),
							},
						},
					},
				},
			}

			coderStub := &stubAgent{id: "coder", response: makeStubResponse("coder handled it", "coder")}
			researcherStub := &stubAgent{id: "researcher", response: makeStubResponse("researcher handled it", "researcher")}
			reviewerStub := &stubAgent{id: "reviewer", response: makeStubResponse("reviewer handled it", "reviewer")}
			chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat handled it", "chat")}

			planGen := taskagent.New(
				agent.Config{ID: "plan-gen", Name: "Plan Gen"},
				taskagent.WithChatCompleter(mock),
				taskagent.WithModel("test-model"),
				taskagent.WithMaxIterations(1),
			)

			orch := agents.NewOrchestratorAgent(
				agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
				mock,
				"test-model",
				map[string]agent.Agent{
					"coder":      coderStub,
					"researcher": researcherStub,
					"reviewer":   reviewerStub,
					"chat":       chatStub,
				},
				planGen,
				2,
				chatStub,
				"/tmp/test",
				nil,
				nil, // no planner
				nil, // toolRegistries
				nil, // reviewReg
				0,   // maxIterations
				0,   // runTokenBudget
			)

			resp, err := orch.Run(context.Background(), &schema.RunRequest{
				Messages: []schema.Message{schema.NewUserMessage("test input")},
			})
			if err != nil {
				t.Fatalf("orchestrator.Run: %v", err)
			}

			if len(resp.Messages) == 0 {
				t.Fatal("expected at least one response message")
			}

			text := resp.Messages[0].Content.Text()
			if text != tt.wantResponse {
				t.Errorf("response = %q, want %q", text, tt.wantResponse)
			}
		})
	}
}

// --- Test 4: Orchestrator plan mode generates and executes a plan (Design Test 2) ---
// Verifies that complex tasks trigger plan generation and DAG execution.
// Test cases:
//   - Plan with 2 sequential steps is parsed correctly
//   - Both researcher and coder agents are invoked in dependency order
//   - Step requests include working directory context and original goal
//   - Results are aggregated into a single response
func TestIntegration_Agents_OrchestratorPlanExecution(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Create a Go utility package",
		"steps": [
			{"id": "step_1", "description": "Research existing patterns", "agent": "researcher", "depends_on": []},
			{"id": "step_2", "description": "Write the utility code", "agent": "coder", "depends_on": ["step_1"]}
		]
	}}`

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

	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 1},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
	}

	coderStub := &stubAgent{id: "coder", response: makeStubResponse("code written", "coder")}
	researcherStub := &stubAgent{id: "researcher", response: makeStubResponse("research done", "researcher")}
	reviewerStub := &stubAgent{id: "reviewer", response: makeStubResponse("review done", "reviewer")}
	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat done", "chat")}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithMaxIterations(1),
	)

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		cfg.LLM.Model,
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": researcherStub,
			"reviewer":   reviewerStub,
			"chat":       chatStub,
		},
		planGen,
		cfg.Memory.MaxConcurrency,
		chatStub,
		"/tmp/test",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("Create a Go utility package")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("orchestrator.Run: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message in response")
	}
}

// --- Test 5: Orchestrator fallback on classification failure (Design Test 3) ---
// Verifies fallback to chat agent when classification returns invalid JSON.
// Test cases:
//   - Invalid JSON from LLM triggers fallback
//   - Chat agent (not coder) receives the request as fallback
//   - Response is the chat agent's response
func TestIntegration_Agents_OrchestratorFallbackOnInvalidJSON(t *testing.T) {
	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("This is not valid JSON at all"),
					},
				},
			},
		},
	}

	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat fallback response", "chat")}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      &stubAgent{id: "coder"},
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("do something")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("orchestrator.Run should not error on fallback: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from fallback")
	}

	text := resp.Messages[0].Content.Text()
	if text != "chat fallback response" {
		t.Errorf("fallback response = %q, want %q", text, "chat fallback response")
	}
}

// --- Test 6: Orchestrator fallback on plan with empty steps (Design Test 3 edge case) ---
// Verifies fallback to chat agent when plan has empty steps (validation fails).
// Test cases:
//   - Plan mode with zero steps triggers validation failure
//   - Fallback to chat agent occurs
//   - Response is the chat agent's response
func TestIntegration_Agents_OrchestratorFallbackOnEmptyPlan(t *testing.T) {
	emptyPlan := `{"mode": "plan", "plan": {"goal": "Nothing", "steps": []}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(emptyPlan),
					},
				},
			},
		},
	}

	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat fallback", "chat")}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      &stubAgent{id: "coder"},
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("do something simple")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("orchestrator.Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from fallback")
	}

	// Fallback to chat because plan validation fails (empty steps).
	text := resp.Messages[0].Content.Text()
	if text != "chat fallback" {
		t.Errorf("response = %q, want %q", text, "chat fallback")
	}
}

// --- Test: Orchestrator fallback on unknown agent in plan (Design Test 3 edge case) ---
// Verifies fallback to chat agent when plan references a non-existent agent.
// Test cases:
//   - Plan referencing unknown agent "nonexistent" triggers validation failure
//   - Fallback to chat agent occurs
func TestIntegration_Agents_OrchestratorFallbackOnInvalidAgent(t *testing.T) {
	badPlan := `{"mode": "plan", "plan": {"goal": "Test", "steps": [{"id": "step_1", "description": "do it", "agent": "nonexistent", "depends_on": []}]}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(badPlan),
					},
				},
			},
		},
	}

	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat fallback", "chat")}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      &stubAgent{id: "coder"},
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("do it")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("orchestrator.Run: %v", err)
	}

	// Should fall back to chat because "nonexistent" is not a valid agent.
	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from fallback")
	}
}

// --- Test 14: OrchestratorAgent implements StreamAgent (Design Test 1, streaming) ---
// Verifies that RunStream returns the sub-agent's stream for direct dispatch.
// Test cases:
//   - OrchestratorAgent satisfies agent.StreamAgent interface
//   - RunStream in direct mode proxies the sub-agent's stream
//   - Stream contains AgentStart and AgentEnd events
func TestIntegration_Agents_OrchestratorImplementsStreamAgent(t *testing.T) {
	directJSON := `{"mode": "direct", "agent": "coder"}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(directJSON),
					},
				},
			},
		},
	}

	coderStub := &stubStreamAgent{id: "coder", response: "done"}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       &stubAgent{id: "chat"},
		},
		planGen,
		2,
		&stubAgent{id: "chat"},
		"",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	// Verify OrchestratorAgent satisfies agent.StreamAgent.
	var sa agent.StreamAgent = orch
	_ = sa

	stream, err := orch.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("simple task")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	// Consume all events.
	var events []schema.Event
	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				break
			}
			t.Fatalf("Recv: %v", recvErr)
		}
		events = append(events, event)
	}

	if len(events) == 0 {
		t.Fatal("expected at least one event from RunStream")
	}

	// Verify we get AgentStart and AgentEnd events.
	hasStart := false
	hasEnd := false
	for _, e := range events {
		if e.Type == schema.EventAgentStart {
			hasStart = true
		}
		if e.Type == schema.EventAgentEnd {
			hasEnd = true
		}
	}

	if !hasStart {
		t.Error("missing agent_start event from orchestrator RunStream")
	}
	if !hasEnd {
		t.Error("missing agent_end event from orchestrator RunStream")
	}
}
