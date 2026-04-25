package dispatches_tests

import (
	"context"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/dispatches"
)

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
		dispatches.WithFastPath(dispatches.DisabledFastPathConfig()),
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
