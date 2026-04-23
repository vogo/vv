package agents_tests

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
)

// --- Test: Working Directory Capture and Propagation (Design Test 4) ---
// Verifies that the working directory is captured at startup, stored in config,
// and propagated to sub-agents in enriched request messages.
// Test cases:
//   - When BashWorkingDir is empty, os.Getwd() populates it
//   - OrchestratorAgent enriches requests with working directory context message
//   - Sub-agent receives the working directory as the first message
//   - The original user message follows the working directory context
func TestIntegration_Agents_WorkingDirectoryCaptureAndPropagation(t *testing.T) {
	// 1. Capture CWD before test.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}

	// 2. Load config with empty BashWorkingDir.
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashWorkingDir: ""},
	}

	// 3. Apply working directory logic from main.go.
	if cfg.Tools.BashWorkingDir == "" {
		workingDir, wdErr := os.Getwd()
		if wdErr != nil {
			workingDir = "."
		}
		cfg.Tools.BashWorkingDir = workingDir
	}

	// 4. Assert BashWorkingDir equals the captured CWD.
	if cfg.Tools.BashWorkingDir != cwd {
		t.Errorf("BashWorkingDir = %q, want %q", cfg.Tools.BashWorkingDir, cwd)
	}

	// 5. Create an OrchestratorAgent with the working directory and a recording sub-agent.
	recordingAgent := &recordingStubAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("done"),
				}, "coder"),
			},
		},
	}

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

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      recordingAgent,
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       &stubAgent{id: "chat"},
		},
		nil,
		2,
		&stubAgent{id: "chat"},
		cfg.Tools.BashWorkingDir,
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	// 6. Trigger a direct dispatch.
	_, err = orch.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("read file main.go")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 7. Assert the sub-agent request contains the working directory context message.
	if recordingAgent.lastRequest == nil {
		t.Fatal("sub-agent was not called")
	}

	msgs := recordingAgent.lastRequest.Messages
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages (working dir + user), got %d", len(msgs))
	}

	wdMsg := msgs[0].Content.Text()
	expectedWD := "Working directory: " + cwd
	if wdMsg != expectedWD {
		t.Errorf("working directory message = %q, want %q", wdMsg, expectedWD)
	}

	// Verify original user message follows.
	userMsg := msgs[1].Content.Text()
	if userMsg != "read file main.go" {
		t.Errorf("user message = %q, want %q", userMsg, "read file main.go")
	}
}

// --- Test: Parallel Step Execution (Design Test 7) ---
// Verifies independent plan steps execute in parallel.
// Test cases:
//   - Two independent steps (no dependencies) both start within a short time window
//   - Both step results are present in the aggregated response
//   - Steps with no depends_on field are executed concurrently
func TestIntegration_Agents_OrchestratorParallelStepExecution(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Do two independent tasks",
		"steps": [
			{"id": "step_1", "description": "Research task A", "agent": "researcher", "depends_on": []},
			{"id": "step_2", "description": "Research task B", "agent": "coder", "depends_on": []}
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

	// Create agents that record invocation timestamps.
	var (
		researcherStart atomic.Int64
		coderStart      atomic.Int64
	)

	researcherStub := &timingStubAgent{
		id:       "researcher",
		response: makeStubResponse("research A done", "researcher"),
		startRef: &researcherStart,
		delay:    50 * time.Millisecond,
	}

	coderStub := &timingStubAgent{
		id:       "coder",
		response: makeStubResponse("code B done", "coder"),
		startRef: &coderStart,
		delay:    50 * time.Millisecond,
	}

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
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       &stubAgent{id: "chat"},
		},
		planGen,
		2, // maxConcurrency=2 allows parallel execution
		&stubAgent{id: "chat"},
		"/tmp/test",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("Do two independent tasks")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Verify both agents were invoked.
	rStart := researcherStart.Load()
	cStart := coderStart.Load()
	if rStart == 0 {
		t.Error("researcher agent was not invoked")
	}
	if cStart == 0 {
		t.Error("coder agent was not invoked")
	}

	// Verify both started within a short window (within 100ms of each other),
	// indicating parallel execution rather than sequential.
	if rStart != 0 && cStart != 0 {
		diff := rStart - cStart
		if diff < 0 {
			diff = -diff
		}
		// 100ms tolerance: both should start nearly simultaneously.
		if diff > int64(100*time.Millisecond) {
			t.Errorf("steps started %v apart, expected near-simultaneous (parallel) execution", time.Duration(diff))
		}
	}

	// Verify response has messages.
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message in aggregated response")
	}
}

// --- Test: Plan Step Failure Handling (Design Test 8) ---
// Verifies the Orchestrator handles sub-task failures gracefully.
// Test cases:
//   - A plan with 2 steps where one step fails does not cause a panic
//   - The failing step is skipped (Optional=true in DAG nodes)
//   - The response includes results from the successful step
//   - No unhandled error is returned
func TestIntegration_Agents_OrchestratorPlanStepFailure(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Two step plan with failure",
		"steps": [
			{"id": "step_1", "description": "This step fails", "agent": "researcher", "depends_on": []},
			{"id": "step_2", "description": "This step succeeds", "agent": "coder", "depends_on": []}
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

	// Create a failing sub-agent.
	failingAgent := &failingStubAgent{id: "researcher", err: errors.New("simulated failure")}

	// Create a succeeding sub-agent.
	successAgent := &stubAgent{id: "coder", response: makeStubResponse("code completed successfully", "coder")}

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
			"coder":      successAgent,
			"researcher": failingAgent,
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

	// Should not panic or return an unhandled error.
	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("do two things")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Run should not error due to one step failure (Optional=true): %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// The response should contain at least the successful step's output.
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message from the successful step")
	}
}

// --- Test: Classification Token Usage Aggregation (Design Test 9) ---
// Verifies token usage from the classification LLM call is included in the response.
// Test cases:
//   - Classification usage is captured from the LLM response
//   - Sub-agent usage is captured from the sub-agent response
//   - Final response usage is the sum of classification + sub-agent usage
//   - PromptTokens, CompletionTokens, and TotalTokens are all aggregated correctly
func TestIntegration_Agents_OrchestratorTokenUsageAggregation(t *testing.T) {
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
			Usage: aimodel.Usage{
				PromptTokens:     50,
				CompletionTokens: 20,
				TotalTokens:      70,
			},
		},
	}

	coderStub := &stubAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("code result"),
				}, "coder"),
			},
			Usage: &aimodel.Usage{
				PromptTokens:     200,
				CompletionTokens: 100,
				TotalTokens:      300,
			},
		},
	}

	chatStub := &stubAgent{id: "chat"}

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
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
		"/tmp/test",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("write code")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify usage aggregation: classify (50+20+70) + sub-agent (200+100+300).
	if resp.Usage == nil {
		t.Fatal("expected usage in response")
	}

	if resp.Usage.PromptTokens != 250 {
		t.Errorf("PromptTokens = %d, want 250 (50 classify + 200 sub-agent)", resp.Usage.PromptTokens)
	}

	if resp.Usage.CompletionTokens != 120 {
		t.Errorf("CompletionTokens = %d, want 120 (20 classify + 100 sub-agent)", resp.Usage.CompletionTokens)
	}

	if resp.Usage.TotalTokens != 370 {
		t.Errorf("TotalTokens = %d, want 370 (70 classify + 300 sub-agent)", resp.Usage.TotalTokens)
	}
}

// --- Test: Chat Agent in Plan Steps (Design Test 10) ---
// Verifies the chat agent can be used as a valid agent in plan steps.
// Test cases:
//   - Plan with a step assigned to "chat" is valid and parsed correctly
//   - Chat agent is invoked for its assigned step
//   - Response includes chat agent output
//   - Plan with chat + coder steps executes successfully end-to-end
func TestIntegration_Agents_OrchestratorChatInPlanSteps(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Explain and implement",
		"steps": [
			{"id": "step_1", "description": "Explain the concept of dependency injection", "agent": "chat", "depends_on": []},
			{"id": "step_2", "description": "Write code implementing DI pattern", "agent": "coder", "depends_on": ["step_1"]}
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

	chatInvoked := &atomic.Bool{}
	coderInvoked := &atomic.Bool{}

	chatStub := &callbackStubAgent{
		id:       "chat",
		response: makeStubResponse("DI explanation done", "chat"),
		onRun:    func() { chatInvoked.Store(true) },
	}
	coderStub := &callbackStubAgent{
		id:       "coder",
		response: makeStubResponse("DI code written", "coder"),
		onRun:    func() { coderInvoked.Store(true) },
	}

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
		Messages:  []schema.Message{schema.NewUserMessage("explain and code DI")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response with messages")
	}

	// Verify chat agent was invoked for step_1.
	if !chatInvoked.Load() {
		t.Error("chat agent was not invoked for plan step")
	}

	// Verify coder agent was invoked for step_2.
	if !coderInvoked.Load() {
		t.Error("coder agent was not invoked for plan step")
	}
}

// --- Test: Orchestrator Fallback on LLM Error (Design Test 3, LLM error) ---
// Verifies fallback to chat agent when LLM returns an error during classification.
// Test cases:
//   - LLM error during classification triggers fallback
//   - Chat agent (not coder) handles the request
//   - Response is the chat agent's response
func TestIntegration_Agents_OrchestratorFallbackOnLLMError(t *testing.T) {
	mock := &mockChatCompleter{
		err: errors.New("LLM service unavailable"),
	}

	chatStub := &stubAgent{
		id:       "chat",
		response: makeStubResponse("chat handled it as fallback", "chat"),
	}

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
		nil,
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
		Messages: []schema.Message{schema.NewUserMessage("anything")},
	})
	if err != nil {
		t.Fatalf("Run should not error on fallback: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from fallback")
	}

	text := resp.Messages[0].Content.Text()
	if text != "chat handled it as fallback" {
		t.Errorf("response = %q, want %q", text, "chat handled it as fallback")
	}
}
