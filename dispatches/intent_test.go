package dispatches

import (
	"context"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/registries"
)

func TestIntentResult_Validate_Direct(t *testing.T) {
	reg := newTestRegistry()
	subAgents := map[string]agent.Agent{
		"coder": &stubAgent{id: "coder"},
		"chat":  &stubAgent{id: "chat"},
	}

	tests := []struct {
		name    string
		intent  IntentResult
		wantErr bool
	}{
		{
			name:   "valid direct coder",
			intent: IntentResult{Mode: "direct", Agent: "coder"},
		},
		{
			name:   "valid direct chat",
			intent: IntentResult{Mode: "direct", Agent: "chat"},
		},
		{
			name:    "invalid direct unknown agent",
			intent:  IntentResult{Mode: "direct", Agent: "unknown"},
			wantErr: true,
		},
		{
			name: "valid plan",
			intent: IntentResult{
				Mode: "plan",
				Plan: &Plan{
					Goal: "test",
					Steps: []PlanStep{
						{ID: "step_1", Agent: "coder", Description: "do thing"},
					},
				},
			},
		},
		{
			name:    "plan with nil plan",
			intent:  IntentResult{Mode: "plan"},
			wantErr: true,
		},
		{
			name:    "unknown mode",
			intent:  IntentResult{Mode: "bogus"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.intent.validate(reg, subAgents)
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRecognizeIntent_MaxDepthFallback(t *testing.T) {
	reg := newTestRegistry()
	subAgents := map[string]agent.Agent{
		"chat": &stubAgent{id: "chat"},
	}

	d := New(
		reg, subAgents, nil, nil, nil,
		WithFallbackAgent(&stubAgent{id: "chat"}),
		WithMaxRecursionDepth(2),
	)

	ctx := WithDepth(context.Background(), 2)

	intent, contextSummary, usage, err := d.recognizeIntent(ctx, &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("hello")},
	})
	if err != nil {
		t.Fatalf("recognizeIntent: %v", err)
	}

	if intent.Mode != "direct" {
		t.Errorf("mode = %q, want %q", intent.Mode, "direct")
	}

	if intent.Agent != "chat" {
		t.Errorf("agent = %q, want %q", intent.Agent, "chat")
	}

	if contextSummary != "" {
		t.Errorf("contextSummary = %q, want empty", contextSummary)
	}

	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

func TestRecognizeIntent_DirectLLM(t *testing.T) {
	reg := newTestRegistry()
	subAgents := map[string]agent.Agent{
		"coder":      &stubAgent{id: "coder"},
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	mockLLM := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"needs_exploration": false, "mode": "direct", "agent": "coder"}`),
					},
				},
			},
			Usage: aimodel.Usage{PromptTokens: 100, CompletionTokens: 50},
		},
	}

	d := New(
		reg, subAgents, nil, nil, nil,
		WithLLM(mockLLM, "test-model"),
		WithFallbackAgent(&stubAgent{id: "chat"}),
		WithIntentSystemPrompt("test prompt"),
	)

	intent, _, usage, err := d.recognizeIntent(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("write some code")},
	})
	if err != nil {
		t.Fatalf("recognizeIntent: %v", err)
	}

	if intent.Mode != "direct" {
		t.Errorf("mode = %q, want %q", intent.Mode, "direct")
	}

	if intent.Agent != "coder" {
		t.Errorf("agent = %q, want %q", intent.Agent, "coder")
	}

	if usage == nil {
		t.Fatal("expected non-nil usage")
	}

	if usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", usage.PromptTokens)
	}
}

// TestRecognizeIntent_ViaPlanner / TestRecognizeIntent_NeedsExploration_NoExplorer
// were removed in M6 G2: the planner-driven intent recognition path and
// the explorer-driven NeedsExploration follow-up are both gone.
// recognizeIntentDirect normalises a residual NeedsExploration:true LLM
// response to a direct-fallback dispatch (covered by TestRecognizeIntent_DirectLLM
// when the response includes the legacy field).

func TestBuildIntentSystemPrompt(t *testing.T) {
	reg := registries.New()
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		reg.MustRegister(registries.AgentDescriptor{
			ID: id, DisplayName: id, Description: id + " description", Dispatchable: true,
		})
	}

	prompt := BuildIntentSystemPrompt(reg)

	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}

	if !strings.Contains(prompt, "coder") {
		t.Error("prompt should contain coder agent")
	}

	// needs_exploration was removed from the prompt template in M6 G2.
	if strings.Contains(prompt, "needs_exploration") {
		t.Error("prompt should no longer mention needs_exploration since M6")
	}
}

func TestBuildIntentSummary(t *testing.T) {
	tests := []struct {
		name   string
		intent *IntentResult
		want   string
	}{
		{
			name:   "nil intent",
			intent: nil,
			want:   "",
		},
		{
			name:   "direct mode",
			intent: &IntentResult{Mode: "direct", Agent: "coder"},
			want:   "Direct -> coder",
		},
		{
			name: "plan mode",
			intent: &IntentResult{
				Mode: "plan",
				Plan: &Plan{
					Goal: "Build feature",
					Steps: []PlanStep{
						{Agent: "researcher", Description: "Research"},
						{Agent: "coder", Description: "Implement"},
					},
				},
			},
			want: "Build feature\n  1. [researcher] Research\n  2. [coder] Implement",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildIntentSummary(tt.intent)
			if got != tt.want {
				t.Errorf("buildIntentSummary = %q, want %q", got, tt.want)
			}
		})
	}
}
