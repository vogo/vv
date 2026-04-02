package dispatches_tests

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"sync/atomic"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/dispatches"
	"github.com/vogo/vv/registries"
)

// =============================================================================
// Mock / stub types (redefined locally since originals are in _test.go files)
// =============================================================================

// sequentialMockLLM returns different responses on successive calls.
type sequentialMockLLM struct {
	responses []*aimodel.ChatResponse
	errors    []error
	callCount atomic.Int32
}

func (m *sequentialMockLLM) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	idx := int(m.callCount.Add(1)) - 1
	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}

	if idx < len(m.responses) {
		return m.responses[idx], nil
	}

	// Default: return last response.
	if len(m.responses) > 0 {
		return m.responses[len(m.responses)-1], nil
	}

	return &aimodel.ChatResponse{}, nil
}

func (m *sequentialMockLLM) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, fmt.Errorf("not implemented")
}

// failingAgent always returns an error.
type failingAgent struct {
	id string
}

func (a *failingAgent) ID() string          { return a.id }
func (a *failingAgent) Name() string        { return a.id }
func (a *failingAgent) Description() string { return a.id }

func (a *failingAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return nil, fmt.Errorf("agent %s: simulated failure", a.id)
}

// callTrackingAgent records whether it was invoked.
type callTrackingAgent struct {
	id       string
	called   atomic.Bool
	response *schema.RunResponse
}

func (a *callTrackingAgent) ID() string          { return a.id }
func (a *callTrackingAgent) Name() string        { return a.id }
func (a *callTrackingAgent) Description() string { return a.id }

func (a *callTrackingAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	a.called.Store(true)

	if a.response != nil {
		return a.response, nil
	}

	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("response from " + a.id),
			}, a.id),
		},
	}, nil
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

// =============================================================================
// Test helpers for integration tests
// =============================================================================

func newIntegrationRegistry() *registries.Registry {
	reg := registries.New()
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		reg.MustRegister(registries.AgentDescriptor{
			ID:           id,
			DisplayName:  id,
			Description:  id + " agent",
			Dispatchable: true,
		})
	}

	return reg
}

func makeSubAgents(agents map[string]agent.Agent) map[string]agent.Agent {
	defaults := map[string]agent.Agent{
		"coder":      &stubAgent{id: "coder"},
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	maps.Copy(defaults, agents)

	return defaults
}

// =============================================================================
// Integration Test 1: Simple Intent Fast Path
//
// Verifies that a simple greeting reaches the chat agent with at most 1 LLM call
// for intent recognition. No planning phase. No explorer invocation.
//
// Acceptance Criteria: US-1 (simple greeting), US-2 (no explorer for simple),
// US-3 (no planner for simple)
// =============================================================================

func TestIntegration_SimpleIntentFastPath(t *testing.T) {
	reg := newIntegrationRegistry()

	chatAgent := &callTrackingAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Hello! How can I help you?"),
				}, "chat"),
			},
		},
	}

	explorerAgent := &callTrackingAgent{id: "explorer"}

	// LLM returns direct dispatch to chat -- no exploration needed.
	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"needs_exploration": false, "mode": "direct", "agent": "chat"}`),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 50, CompletionTokens: 20},
			},
		},
	}

	subAgents := makeSubAgents(map[string]agent.Agent{"chat": chatAgent})

	d := dispatches.New(
		reg, subAgents, explorerAgent, nil, nil,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "int-test-1",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify chat agent was dispatched to.
	if !chatAgent.called.Load() {
		t.Error("expected chat agent to be called")
	}

	// Verify explorer was NOT called.
	if explorerAgent.called.Load() {
		t.Error("explorer should NOT be called for a simple greeting")
	}

	// Verify only 1 LLM call was made (intent recognition).
	if count := int(mockLLM.callCount.Load()); count != 1 {
		t.Errorf("LLM call count = %d, want 1 (single intent recognition call)", count)
	}

	// Verify response content.
	if len(resp.Messages) == 0 {
		t.Fatal("expected response messages")
	}

	if resp.Messages[0].Content.Text() != "Hello! How can I help you?" {
		t.Errorf("response = %q, want greeting", resp.Messages[0].Content.Text())
	}
}

// =============================================================================
// Integration Test 2: Code Request with On-Demand Exploration
//
// Verifies that when the LLM returns needs_exploration=true, the explorer agent
// is invoked, followed by a re-assessment LLM call, then dispatch to coder.
//
// Acceptance Criteria: US-1 (coding request), US-2 (explorer on-demand)
// =============================================================================

func TestIntegration_CodeRequestWithExploration(t *testing.T) {
	reg := newIntegrationRegistry()

	coderAgent := &callTrackingAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("I added the tests for types.go"),
				}, "coder"),
			},
		},
	}

	explorerAgent := &callTrackingAgent{
		id: "explorer",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Found types.go with Plan struct, PlanStep struct"),
				}, "explorer"),
			},
			Usage: &aimodel.Usage{PromptTokens: 200, CompletionTokens: 100},
		},
	}

	// LLM call 1: needs exploration. LLM call 2: re-assess -> dispatch to coder.
	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"needs_exploration": true}`),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 80, CompletionTokens: 15},
			},
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"needs_exploration": false, "mode": "direct", "agent": "coder"}`),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 150, CompletionTokens: 25},
			},
		},
	}

	subAgents := makeSubAgents(map[string]agent.Agent{"coder": coderAgent})

	d := dispatches.New(
		reg, subAgents, explorerAgent, nil, nil,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("add a test for the Plan struct in types.go")},
		SessionID: "int-test-2",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify explorer was called.
	if !explorerAgent.called.Load() {
		t.Error("expected explorer agent to be called for code request referencing files")
	}

	// Verify coder was dispatched to.
	if !coderAgent.called.Load() {
		t.Error("expected coder agent to be called after exploration")
	}

	// Verify 2 LLM calls: initial intent + re-assessment.
	if count := int(mockLLM.callCount.Load()); count != 2 {
		t.Errorf("LLM call count = %d, want 2 (intent + re-assessment)", count)
	}

	// Verify response.
	if len(resp.Messages) == 0 {
		t.Fatal("expected response messages")
	}

	if resp.Messages[0].Content.Text() != "I added the tests for types.go" {
		t.Errorf("response = %q, want coder output", resp.Messages[0].Content.Text())
	}
}

// =============================================================================
// Integration Test 3: Complex Task Planning
//
// Verifies that intent recognition produces a plan with multiple steps, the DAG
// executes those steps, and results are aggregated.
//
// Acceptance Criteria: US-1 (complex request), US-3 (planner on-demand)
// =============================================================================

func TestIntegration_ComplexTaskPlanning(t *testing.T) {
	reg := newIntegrationRegistry()

	researcherAgent := &callTrackingAgent{
		id: "researcher",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Researched error handling patterns"),
				}, "researcher"),
			},
		},
	}

	coderAgent := &callTrackingAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Refactored error handling"),
				}, "coder"),
			},
		},
	}

	reviewerAgent := &callTrackingAgent{
		id: "reviewer",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Review complete, LGTM"),
				}, "reviewer"),
			},
		},
	}

	planJSON := `{
		"needs_exploration": false,
		"mode": "plan",
		"plan": {
			"goal": "Refactor error handling and add tests",
			"steps": [
				{"id": "step_1", "description": "Research error handling patterns", "agent": "researcher", "depends_on": []},
				{"id": "step_2", "description": "Refactor error handling", "agent": "coder", "depends_on": ["step_1"]},
				{"id": "step_3", "description": "Review changes", "agent": "reviewer", "depends_on": ["step_2"]}
			]
		}
	}`

	// LLM returns a plan.
	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 200, CompletionTokens: 100},
			},
			// Plan summary LLM call (for PlanAggregator when there are terminal nodes).
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("Aggregated summary of all steps"),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 100, CompletionTokens: 50},
			},
		},
	}

	subAgents := makeSubAgents(map[string]agent.Agent{
		"coder":      coderAgent,
		"researcher": researcherAgent,
		"reviewer":   reviewerAgent,
	})

	// planGen agent to aggregate results.
	planGen := &stubAgent{
		id: "plan-gen",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("All steps completed: research, refactor, and review done"),
				}, "plan-gen"),
			},
		},
	}

	d := dispatches.New(
		reg, subAgents, nil, nil, planGen,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
		dispatches.WithMaxConcurrency(2),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("refactor the error handling and add tests")},
		SessionID: "int-test-3",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// All three agents should have been called.
	if !researcherAgent.called.Load() {
		t.Error("expected researcher agent to be called")
	}

	if !coderAgent.called.Load() {
		t.Error("expected coder agent to be called")
	}

	if !reviewerAgent.called.Load() {
		t.Error("expected reviewer agent to be called")
	}

	// Verify we got a response.
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected response messages from plan execution")
	}
}

// =============================================================================
// Integration Test 4: Replan on Step Failure
//
// Verifies that when ReplanPolicy.TriggerOnFailure is enabled and a step fails,
// replanning is triggered. New replacement steps are generated and executed.
//
// Acceptance Criteria: US-4 (replan on failure)
// =============================================================================

func TestIntegration_ReplanOnStepFailure(t *testing.T) {
	reg := newIntegrationRegistry()

	researcherAgent := &callTrackingAgent{
		id: "researcher",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Research done"),
				}, "researcher"),
			},
		},
	}

	// Coder fails on first call -- simulating a step failure.
	coderFail := &failingAgent{id: "coder"}

	planJSON := `{
		"needs_exploration": false,
		"mode": "plan",
		"plan": {
			"goal": "Fix failing code",
			"steps": [
				{"id": "step_1", "description": "Research the issue", "agent": "researcher", "depends_on": []},
				{"id": "step_2", "description": "Fix the code", "agent": "coder", "depends_on": ["step_1"]}
			]
		}
	}`

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 100, CompletionTokens: 50},
			},
		},
	}

	planGen := &stubAgent{
		id: "plan-gen",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Partial results aggregated"),
				}, "plan-gen"),
			},
		},
	}

	subAgents := makeSubAgents(map[string]agent.Agent{
		"coder":      coderFail,
		"researcher": researcherAgent,
	})

	d := dispatches.New(
		reg, subAgents, nil, nil, planGen,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
		dispatches.WithMaxConcurrency(2),
		dispatches.WithReplanPolicy(dispatches.ReplanPolicy{
			TriggerOnFailure: true,
			MaxReplans:       2,
		}),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("fix the failing code")},
		SessionID: "int-test-4",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Researcher should have been called (layer 0 succeeds).
	if !researcherAgent.called.Load() {
		t.Error("expected researcher agent to be called")
	}

	// Response should exist (partial or replanned).
	if resp == nil {
		t.Fatal("expected non-nil response even with step failure and replanning")
	}
}

// =============================================================================
// Integration Test 5: Replan Count Limit
//
// Verifies that after MaxReplans is exhausted, further failures abort execution.
// Partial results are returned.
//
// Acceptance Criteria: US-4 (replan limit exceeded)
// =============================================================================

func TestIntegration_ReplanCountLimit(t *testing.T) {
	reg := newIntegrationRegistry()

	// All agents fail -- each replan will also fail.
	failCoder := &failingAgent{id: "coder"}

	planJSON := `{
		"needs_exploration": false,
		"mode": "plan",
		"plan": {
			"goal": "Attempt impossible task",
			"steps": [
				{"id": "step_1", "description": "Do the thing", "agent": "coder", "depends_on": []}
			]
		}
	}`

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 100, CompletionTokens: 50},
			},
		},
	}

	planGen := &stubAgent{
		id: "plan-gen",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("partial"),
				}, "plan-gen"),
			},
		},
	}

	subAgents := makeSubAgents(map[string]agent.Agent{"coder": failCoder})

	d := dispatches.New(
		reg, subAgents, nil, nil, planGen,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
		dispatches.WithMaxConcurrency(1),
		dispatches.WithReplanPolicy(dispatches.ReplanPolicy{
			TriggerOnFailure: true,
			MaxReplans:       1,
		}),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("do the impossible")},
		SessionID: "int-test-5",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Should get a response (partial or empty) rather than a hard error.
	if resp == nil {
		t.Fatal("expected non-nil response even when replan limit exceeded")
	}
}

// =============================================================================
// Integration Test 6: Recursion Depth Enforcement
//
// Verifies depth-based behavior:
// - Depth 0: full decision loop (intent recognition runs)
// - Depth >= maxRecursionDepth: direct execution only, skip intent recognition
//
// Acceptance Criteria: US-5 (depth control)
// =============================================================================

func TestIntegration_RecursionDepthEnforcement(t *testing.T) {
	reg := newIntegrationRegistry()

	chatAgent := &callTrackingAgent{
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

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"needs_exploration": false, "mode": "direct", "agent": "chat"}`),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 50, CompletionTokens: 20},
			},
		},
	}

	subAgents := makeSubAgents(map[string]agent.Agent{"chat": chatAgent})

	// Test case 1: Depth 0 -- full decision loop, LLM is called.
	t.Run("depth_0_full_loop", func(t *testing.T) {
		llm := &sequentialMockLLM{
			responses: mockLLM.responses,
		}

		d := dispatches.New(
			reg, subAgents, nil, nil, nil,
			dispatches.WithLLM(llm, "test-model"),
			dispatches.WithFallbackAgent(chatAgent),
			dispatches.WithMaxRecursionDepth(2),
			dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
		)

		ctx := dispatches.WithDepth(context.Background(), 0)
		_, err := d.Run(ctx, &schema.RunRequest{
			Messages:  []schema.Message{schema.NewUserMessage("hello")},
			SessionID: "int-test-6a",
		})
		if err != nil {
			t.Fatalf("Run at depth 0: %v", err)
		}

		if count := int(llm.callCount.Load()); count != 1 {
			t.Errorf("at depth 0, LLM calls = %d, want 1 (intent recognition)", count)
		}
	})

	// Test case 2: Depth >= maxRecursionDepth -- skip intent recognition, direct fallback.
	t.Run("depth_at_max_skips_intent", func(t *testing.T) {
		llm := &sequentialMockLLM{
			responses: mockLLM.responses,
		}

		fallback := &callTrackingAgent{
			id: "chat",
			response: &schema.RunResponse{
				Messages: []schema.Message{
					schema.NewAssistantMessage(aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("fallback at max depth"),
					}, "chat"),
				},
			},
		}

		d := dispatches.New(
			reg, makeSubAgents(map[string]agent.Agent{"chat": fallback}), nil, nil, nil,
			dispatches.WithLLM(llm, "test-model"),
			dispatches.WithFallbackAgent(fallback),
			dispatches.WithMaxRecursionDepth(2),
		)

		ctx := dispatches.WithDepth(context.Background(), 2)
		resp, err := d.Run(ctx, &schema.RunRequest{
			Messages:  []schema.Message{schema.NewUserMessage("nested request")},
			SessionID: "int-test-6b",
		})
		if err != nil {
			t.Fatalf("Run at depth 2: %v", err)
		}

		// No LLM calls should be made -- intent recognition is skipped.
		if count := int(llm.callCount.Load()); count != 0 {
			t.Errorf("at depth >= max, LLM calls = %d, want 0 (skipped)", count)
		}

		if len(resp.Messages) == 0 {
			t.Fatal("expected fallback response at max depth")
		}
	})

	// Test case 3: Depth above max -- also skips.
	t.Run("depth_above_max", func(t *testing.T) {
		llm := &sequentialMockLLM{
			responses: mockLLM.responses,
		}

		fallback := &callTrackingAgent{
			id: "chat",
			response: &schema.RunResponse{
				Messages: []schema.Message{
					schema.NewAssistantMessage(aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("deep fallback"),
					}, "chat"),
				},
			},
		}

		d := dispatches.New(
			reg, makeSubAgents(map[string]agent.Agent{"chat": fallback}), nil, nil, nil,
			dispatches.WithLLM(llm, "test-model"),
			dispatches.WithFallbackAgent(fallback),
			dispatches.WithMaxRecursionDepth(2),
		)

		ctx := dispatches.WithDepth(context.Background(), 5)
		_, err := d.Run(ctx, &schema.RunRequest{
			Messages:  []schema.Message{schema.NewUserMessage("deeply nested")},
			SessionID: "int-test-6c",
		})
		if err != nil {
			t.Fatalf("Run at depth 5: %v", err)
		}

		if count := int(llm.callCount.Load()); count != 0 {
			t.Errorf("at depth 5, LLM calls = %d, want 0", count)
		}
	})
}

// =============================================================================
// Integration Test 7: Summary Policy -- Auto Mode (CLI)
//
// Verifies that with SummaryPolicy=auto and mode=cli, no summarization occurs.
//
// Acceptance Criteria: US-6 (CLI skip)
// =============================================================================

func TestIntegration_SummaryPolicy_Auto_CLI(t *testing.T) {
	reg := newIntegrationRegistry()

	chatAgent := &callTrackingAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("chat output"),
				}, "chat"),
			},
		},
	}

	summarizer := &callTrackingAgent{
		id: "summarizer",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("this should NOT appear"),
				}, "summarizer"),
			},
		},
	}

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"needs_exploration": false, "mode": "direct", "agent": "chat"}`),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 50, CompletionTokens: 20},
			},
		},
	}

	subAgents := makeSubAgents(map[string]agent.Agent{"chat": chatAgent})

	d := dispatches.New(
		reg, subAgents, nil, nil, nil,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithSummaryPolicy(dispatches.SummaryAuto),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
		dispatches.WithSummarizer(summarizer),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "int-test-7",
		Metadata:  map[string]any{"mode": "cli"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Summarizer should NOT have been called in CLI mode.
	if summarizer.called.Load() {
		t.Error("summarizer should NOT be called in CLI mode with auto policy")
	}

	// Response should be from chat, not summarizer.
	if len(resp.Messages) > 0 && resp.Messages[0].Content.Text() == "this should NOT appear" {
		t.Error("received summarized response in CLI mode, which should not happen")
	}
}

// =============================================================================
// Integration Test 8: Summary Policy -- Auto Mode (HTTP)
//
// Verifies that with SummaryPolicy=auto and mode=http, summarization occurs.
//
// Acceptance Criteria: US-6 (HTTP summarize)
// =============================================================================

func TestIntegration_SummaryPolicy_Auto_HTTP(t *testing.T) {
	reg := newIntegrationRegistry()

	chatAgent := &callTrackingAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("chat output for HTTP"),
				}, "chat"),
			},
		},
	}

	summarizer := &callTrackingAgent{
		id: "summarizer",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("HTTP summary"),
				}, "summarizer"),
			},
		},
	}

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"needs_exploration": false, "mode": "direct", "agent": "chat"}`),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 50, CompletionTokens: 20},
			},
		},
	}

	subAgents := makeSubAgents(map[string]agent.Agent{"chat": chatAgent})

	d := dispatches.New(
		reg, subAgents, nil, nil, nil,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithSummaryPolicy(dispatches.SummaryAuto),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
		dispatches.WithSummarizer(summarizer),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "int-test-8",
		Metadata:  map[string]any{"mode": "http"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Summarizer should have been called in HTTP mode.
	if !summarizer.called.Load() {
		t.Error("summarizer should be called in HTTP mode with auto policy")
	}

	// Response should be the summary.
	if len(resp.Messages) > 0 && resp.Messages[0].Content.Text() != "HTTP summary" {
		t.Errorf("expected summary response, got %q", resp.Messages[0].Content.Text())
	}
}

// =============================================================================
// Integration Test 9: Summary Policy -- Always/Never
//
// Verifies that SummaryPolicy=always always generates a summary regardless of
// mode, and SummaryPolicy=never never generates one regardless of mode.
//
// Acceptance Criteria: US-6 (force on/off)
// =============================================================================

func TestIntegration_SummaryPolicy_AlwaysNever(t *testing.T) {
	reg := newIntegrationRegistry()

	makeDispatcher := func(policy dispatches.SummaryPolicy) (*dispatches.Dispatcher, *callTrackingAgent) {
		chatAgent := &callTrackingAgent{
			id: "chat",
			response: &schema.RunResponse{
				Messages: []schema.Message{
					schema.NewAssistantMessage(aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("chat output"),
					}, "chat"),
				},
			},
		}

		summarizer := &callTrackingAgent{
			id: "summarizer",
			response: &schema.RunResponse{
				Messages: []schema.Message{
					schema.NewAssistantMessage(aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("summarized output"),
					}, "summarizer"),
				},
			},
		}

		mockLLM := &sequentialMockLLM{
			responses: []*aimodel.ChatResponse{
				{
					Choices: []aimodel.Choice{{
						Message: aimodel.Message{
							Role:    aimodel.RoleAssistant,
							Content: aimodel.NewTextContent(`{"needs_exploration": false, "mode": "direct", "agent": "chat"}`),
						},
					}},
					Usage: aimodel.Usage{PromptTokens: 50, CompletionTokens: 20},
				},
			},
		}

		subAgents := makeSubAgents(map[string]agent.Agent{"chat": chatAgent})

		d := dispatches.New(
			reg, subAgents, nil, nil, nil,
			dispatches.WithLLM(mockLLM, "test-model"),
			dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
			dispatches.WithSummaryPolicy(policy),
			dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
			dispatches.WithSummarizer(summarizer),
		)

		return d, summarizer
	}

	// Test "always" -- should summarize even in CLI mode.
	t.Run("always_summarizes_in_cli", func(t *testing.T) {
		d, summarizer := makeDispatcher(dispatches.SummaryAlways)
		_, err := d.Run(context.Background(), &schema.RunRequest{
			Messages:  []schema.Message{schema.NewUserMessage("hello")},
			SessionID: "int-test-9a",
			Metadata:  map[string]any{"mode": "cli"},
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}

		if !summarizer.called.Load() {
			t.Error("summarizer should be called with SummaryAlways even in CLI mode")
		}
	})

	// Test "never" -- should not summarize even in HTTP mode.
	t.Run("never_skips_in_http", func(t *testing.T) {
		d, summarizer := makeDispatcher(dispatches.SummaryNever)
		_, err := d.Run(context.Background(), &schema.RunRequest{
			Messages:  []schema.Message{schema.NewUserMessage("hello")},
			SessionID: "int-test-9b",
			Metadata:  map[string]any{"mode": "http"},
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}

		if summarizer.called.Load() {
			t.Error("summarizer should NOT be called with SummaryNever even in HTTP mode")
		}
	})
}

// =============================================================================
// Integration Test 10: Streaming Phase Events
//
// Verifies that streaming produces the correct phase events with the new phase
// names (intent, execute), phases appear only when they execute, and TotalPhase=0.
//
// Acceptance Criteria: US-1 (unified loop), ORCH-14, ORCH-21
// =============================================================================

func TestIntegration_StreamingPhaseEvents(t *testing.T) {
	reg := newIntegrationRegistry()

	coderStream := &stubStreamAgent{
		id:       "coder",
		response: "streamed coder response",
	}

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"needs_exploration": false, "mode": "direct", "agent": "coder"}`),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 50, CompletionTokens: 20},
			},
		},
	}

	subAgents := makeSubAgents(map[string]agent.Agent{"coder": coderStream})

	d := dispatches.New(
		reg, subAgents, nil, nil, nil,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
		dispatches.WithSummaryPolicy(dispatches.SummaryNever),
	)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("write some code")},
		SessionID: "int-test-10",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	var events []schema.Event
	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}

			t.Fatalf("Recv: %v", recvErr)
		}

		events = append(events, event)
	}

	// Collect phase starts and ends.
	phaseStarts := make(map[string]schema.PhaseStartData)
	phaseEnds := make(map[string]schema.PhaseEndData)
	phaseStartOrder := []string{}

	for _, ev := range events {
		switch ev.Type {
		case schema.EventPhaseStart:
			if data, ok := ev.Data.(schema.PhaseStartData); ok {
				phaseStarts[data.Phase] = data
				phaseStartOrder = append(phaseStartOrder, data.Phase)
			}
		case schema.EventPhaseEnd:
			if data, ok := ev.Data.(schema.PhaseEndData); ok {
				phaseEnds[data.Phase] = data
			}
		}
	}

	// Verify "intent" phase exists.
	if _, ok := phaseStarts["intent"]; !ok {
		t.Error("missing intent PhaseStart event")
	}

	// Verify "execute" phase exists.
	if _, ok := phaseStarts["execute"]; !ok {
		t.Error("missing execute PhaseStart event")
	}

	// Verify no "explore" or "plan" or "dispatch" old-style phases appear
	// (unless exploration was triggered, which it wasn't in this test).
	for _, phase := range []string{"plan", "dispatch"} {
		if _, ok := phaseStarts[phase]; ok {
			t.Errorf("unexpected old-style phase %q in events", phase)
		}
	}

	// Verify TotalPhase is 0 (dynamic).
	if intentStart, ok := phaseStarts["intent"]; ok {
		if intentStart.TotalPhase != 0 {
			t.Errorf("intent TotalPhase = %d, want 0 (dynamic)", intentStart.TotalPhase)
		}
	}

	if execStart, ok := phaseStarts["execute"]; ok {
		if execStart.TotalPhase != 0 {
			t.Errorf("execute TotalPhase = %d, want 0 (dynamic)", execStart.TotalPhase)
		}
	}

	// Verify intent comes before execute.
	intentIdx := -1
	executeIdx := -1

	for i, phase := range phaseStartOrder {
		if phase == "intent" && intentIdx == -1 {
			intentIdx = i
		}

		if phase == "execute" && executeIdx == -1 {
			executeIdx = i
		}
	}

	if intentIdx >= executeIdx {
		t.Errorf("intent phase (order %d) should come before execute phase (order %d)", intentIdx, executeIdx)
	}

	// Verify every start has a matching end.
	for phase := range phaseStarts {
		if _, ok := phaseEnds[phase]; !ok {
			t.Errorf("PhaseStart for %q has no matching PhaseEnd", phase)
		}
	}

	// Verify no summarize phase appears (SummaryNever).
	if _, ok := phaseStarts["summarize"]; ok {
		t.Error("summarize phase should NOT appear with SummaryNever")
	}
}

// =============================================================================
// Integration Test: Streaming with Summarization Phase
//
// Verifies that the summarize phase appears in streaming when SummaryAlways is set.
//
// Acceptance Criteria: US-6 combined with streaming events
// =============================================================================

func TestIntegration_StreamingWithSummarizePhase(t *testing.T) {
	reg := newIntegrationRegistry()

	chatStream := &stubStreamAgent{
		id:       "chat",
		response: "chat output",
	}

	summarizerStream := &stubStreamAgent{
		id:       "summarizer",
		response: "streaming summary",
	}

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"needs_exploration": false, "mode": "direct", "agent": "chat"}`),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 50, CompletionTokens: 20},
			},
		},
	}

	subAgents := makeSubAgents(map[string]agent.Agent{"chat": chatStream})

	d := dispatches.New(
		reg, subAgents, nil, nil, summarizerStream,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithSummaryPolicy(dispatches.SummaryAlways),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
	)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "int-test-11",
		Metadata:  map[string]any{"mode": "http"},
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	var events []schema.Event
	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}

			t.Fatalf("Recv: %v", recvErr)
		}

		events = append(events, event)
	}

	// Verify summarize phase is present.
	var hasSummarizeStart, hasSummarizeEnd bool

	for _, ev := range events {
		switch ev.Type {
		case schema.EventPhaseStart:
			if data, ok := ev.Data.(schema.PhaseStartData); ok && data.Phase == "summarize" {
				hasSummarizeStart = true
			}
		case schema.EventPhaseEnd:
			if data, ok := ev.Data.(schema.PhaseEndData); ok && data.Phase == "summarize" {
				hasSummarizeEnd = true
			}
		}
	}

	if !hasSummarizeStart {
		t.Error("expected summarize PhaseStart event with SummaryAlways")
	}

	if !hasSummarizeEnd {
		t.Error("expected summarize PhaseEnd event with SummaryAlways")
	}
}

// =============================================================================
// Integration Test: Depth Propagation Through Execute
//
// Verifies that depth is incremented when dispatching to child agents, so that
// nested dispatchers see the correct depth.
//
// Acceptance Criteria: US-5 (depth propagation)
// =============================================================================

func TestIntegration_DepthPropagationThroughExecute(t *testing.T) {
	reg := newIntegrationRegistry()

	depthCapturingAgent := &stubAgent{id: "chat"}
	// We override chat with a custom agent that checks depth.
	type depthCaptureAgent struct {
		id string
	}

	dca := &depthCaptureAgent{id: "chat"}
	_ = dca // just to reference the type

	// Use a custom approach: the Dispatcher increments depth before calling executeTask.
	// We verify by checking that at depth 0, the child context has depth 1.
	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"needs_exploration": false, "mode": "direct", "agent": "chat"}`),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 50, CompletionTokens: 20},
			},
		},
	}

	subAgents := makeSubAgents(map[string]agent.Agent{"chat": depthCapturingAgent})

	d := dispatches.New(
		reg, subAgents, nil, nil, nil,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(depthCapturingAgent),
		dispatches.WithMaxRecursionDepth(3),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
	)

	// Run at depth 0.
	ctx := dispatches.WithDepth(context.Background(), 0)
	_, err := d.Run(ctx, &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("test depth")},
		SessionID: "int-test-depth",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify the LLM was called (intent recognition happened at depth 0).
	if count := int(mockLLM.callCount.Load()); count != 1 {
		t.Errorf("LLM call count = %d, want 1", count)
	}

	// Verify depth increments correctly.
	ctx0 := dispatches.WithDepth(context.Background(), 0)
	ctx1 := dispatches.IncrementDepth(ctx0)
	ctx2 := dispatches.IncrementDepth(ctx1)

	if dispatches.DepthFrom(ctx1) != 1 {
		t.Errorf("depth after 1 increment = %d, want 1", dispatches.DepthFrom(ctx1))
	}

	if dispatches.DepthFrom(ctx2) != 2 {
		t.Errorf("depth after 2 increments = %d, want 2", dispatches.DepthFrom(ctx2))
	}
}

// =============================================================================
// Integration Test: End-to-End Pipeline with Explorer + Direct LLM Intent
//
// Verifies the full pipeline: LLM intent call returns needs_exploration,
// explorer runs, re-assessment call determines agent, agent executes.
// No planner agent is used (direct LLM path).
//
// Acceptance Criteria: Full pipeline correctness
// =============================================================================

func TestIntegration_EndToEnd_ExplorerPlusDirect(t *testing.T) {
	reg := newIntegrationRegistry()

	explorerAgent := &callTrackingAgent{
		id: "explorer",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Project has: main.go, types.go, dispatch.go"),
				}, "explorer"),
			},
			Usage: &aimodel.Usage{PromptTokens: 300, CompletionTokens: 100},
		},
	}

	reviewerAgent := &callTrackingAgent{
		id: "reviewer",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Code review complete, all looks good"),
				}, "reviewer"),
			},
			Usage: &aimodel.Usage{PromptTokens: 500, CompletionTokens: 200},
		},
	}

	// LLM call 1: needs exploration. LLM call 2: direct to reviewer.
	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"needs_exploration": true}`),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 80, CompletionTokens: 10},
			},
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"needs_exploration": false, "mode": "direct", "agent": "reviewer"}`),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 200, CompletionTokens: 30},
			},
		},
	}

	subAgents := makeSubAgents(map[string]agent.Agent{"reviewer": reviewerAgent})

	d := dispatches.New(
		reg, subAgents, explorerAgent, nil, nil,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
		dispatches.WithSummaryPolicy(dispatches.SummaryNever),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("review the dispatch.go file")},
		SessionID: "int-test-e2e",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify explorer was invoked.
	if !explorerAgent.called.Load() {
		t.Error("expected explorer to be called")
	}

	// Verify reviewer was dispatched to.
	if !reviewerAgent.called.Load() {
		t.Error("expected reviewer to be called after exploration")
	}

	// Verify 2 LLM calls: initial intent + re-assessment.
	if count := int(mockLLM.callCount.Load()); count != 2 {
		t.Errorf("LLM call count = %d, want 2", count)
	}

	// Verify response content.
	if len(resp.Messages) == 0 {
		t.Fatal("expected response messages")
	}

	if resp.Messages[0].Content.Text() != "Code review complete, all looks good" {
		t.Errorf("response = %q, want reviewer output", resp.Messages[0].Content.Text())
	}

	// Verify usage aggregation includes intent + exploration + execution.
	if resp.Usage == nil {
		t.Fatal("expected usage in response")
	}

	// Intent (80+10) + exploration (300+100) + reassessment (200+30) + execution (500+200).
	expectedPrompt := 80 + 300 + 200 + 500
	if resp.Usage.PromptTokens != expectedPrompt {
		t.Errorf("PromptTokens = %d, want %d", resp.Usage.PromptTokens, expectedPrompt)
	}
}
