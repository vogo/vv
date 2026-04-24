package dispatches_tests

import (
	"context"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/dispatches"
)

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
			dispatches.WithFastPath(dispatches.DisabledFastPathConfig()),
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
