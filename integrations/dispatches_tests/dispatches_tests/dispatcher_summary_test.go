package dispatches_tests

import (
	"context"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/dispatches"
)

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
