package dispatches_tests

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/dispatches"
)

// fastPathPhaseName mirrors the unexported fastPathPhase constant in the
// dispatches package. Stream consumers see this literal on EventPhaseStart /
// EventPhaseEnd events when the heuristic short-circuit fires.
const fastPathPhaseName = "fast_path"

// intentPhaseName is the phase emitted when the regular intent LLM path runs.
const intentPhaseName = "intent"

// newFastPathIntegrationDispatcher builds a Dispatcher wired for the M1
// acceptance-criteria tests: chat / coder are call-tracking agents, every other
// role uses stubAgent, and fast-path defaults are on unless overridden.
func newFastPathIntegrationDispatcher(
	t *testing.T,
	fp dispatches.FastPathConfig,
	chat, coder agent.Agent,
	mockLLM aimodel.ChatCompleter,
) *dispatches.Dispatcher {
	t.Helper()

	reg := newIntegrationRegistry()

	subAgents := makeSubAgents(map[string]agent.Agent{
		"chat":  chat,
		"coder": coder,
	})

	return dispatches.New(
		reg, subAgents, nil, nil, nil,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
		dispatches.WithSummaryPolicy(dispatches.SummaryNever),
		dispatches.WithFastPath(fp),
	)
}

// TestFastPathIntegration_GreetingShortCircuitsToChat verifies AC-1.1: a plain
// "hello" greeting goes straight to the chat agent with zero LLM calls on the
// intent path.
func TestFastPathIntegration_GreetingShortCircuitsToChat(t *testing.T) {
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
	coderAgent := &callTrackingAgent{id: "coder"}

	// Any intent LLM call would bump callCount and reveal a regression.
	mockLLM := &sequentialMockLLM{}

	d := newFastPathIntegrationDispatcher(t, dispatches.DefaultFastPathConfig(), chatAgent, coderAgent, mockLLM)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "fp-ac-1-1",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := int(mockLLM.callCount.Load()); got != 0 {
		t.Errorf("intent LLM call count = %d, want 0 for fast-path greeting", got)
	}

	if !chatAgent.called.Load() {
		t.Error("expected chat agent to be invoked on fast-path hit")
	}

	if coderAgent.called.Load() {
		t.Error("coder agent must not be called for a greeting")
	}

	if len(resp.Messages) == 0 || resp.Messages[0].Content.Text() != "Hello! How can I help you?" {
		t.Errorf("response = %+v, want chat greeting", resp.Messages)
	}
}

// TestFastPathIntegration_StreamEmitsFastPathPhase verifies AC-1.2 and AC-5.2:
// streaming mode emits phase_start("fast_path") + phase_end("fast_path") with
// zero token / tool-call counts and must NOT emit phase_start("intent").
func TestFastPathIntegration_StreamEmitsFastPathPhase(t *testing.T) {
	chatStream := &stubStreamAgent{id: "chat", response: "hi there"}
	coderAgent := &callTrackingAgent{id: "coder"}
	mockLLM := &sequentialMockLLM{}

	reg := newIntegrationRegistry()
	subAgents := makeSubAgents(map[string]agent.Agent{
		"chat":  chatStream,
		"coder": coderAgent,
	})

	d := dispatches.New(
		reg, subAgents, nil, nil, nil,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
		dispatches.WithSummaryPolicy(dispatches.SummaryNever),
		dispatches.WithFastPath(dispatches.DefaultFastPathConfig()),
	)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "fp-ac-1-2",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	var (
		gotFastPathStart bool
		gotFastPathEnd   bool
		gotIntentStart   bool
		fastEndData      schema.PhaseEndData
		phaseOrder       []string
	)

	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}

			t.Fatalf("Recv: %v", recvErr)
		}

		switch ev.Type {
		case schema.EventPhaseStart:
			if data, ok := ev.Data.(schema.PhaseStartData); ok {
				phaseOrder = append(phaseOrder, data.Phase)
				switch data.Phase {
				case fastPathPhaseName:
					gotFastPathStart = true
				case intentPhaseName:
					gotIntentStart = true
				}
			}
		case schema.EventPhaseEnd:
			if data, ok := ev.Data.(schema.PhaseEndData); ok && data.Phase == fastPathPhaseName {
				gotFastPathEnd = true
				fastEndData = data
			}
		}
	}

	if !gotFastPathStart {
		t.Error("expected phase_start event with Phase=fast_path")
	}

	if !gotFastPathEnd {
		t.Error("expected phase_end event with Phase=fast_path")
	}

	if gotIntentStart {
		t.Errorf("did not expect intent phase_start when fast-path fires; saw phases: %v", phaseOrder)
	}

	if fastEndData.ToolCalls != 0 || fastEndData.PromptTokens != 0 || fastEndData.CompletionTokens != 0 {
		t.Errorf("fast-path phase_end should report zero cost, got %+v", fastEndData)
	}

	if got := int(mockLLM.callCount.Load()); got != 0 {
		t.Errorf("intent LLM call count = %d, want 0 in stream fast-path", got)
	}
}

// TestFastPathIntegration_LongGreetingFallsBackToIntent verifies AC-1.4: a
// greeting that exceeds the 60-char rune cap must skip the fast path and go
// through the normal intent flow (resulting in at least one intent LLM call).
func TestFastPathIntegration_LongGreetingFallsBackToIntent(t *testing.T) {
	chatAgent := &callTrackingAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Sure, happy to help with that long greeting."),
				}, "chat"),
			},
		},
	}
	coderAgent := &callTrackingAgent{id: "coder"}

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

	d := newFastPathIntegrationDispatcher(t, dispatches.DefaultFastPathConfig(), chatAgent, coderAgent, mockLLM)

	// Craft a greeting that still starts with "hello" but exceeds 60 runes.
	longGreeting := "hello there my dear friend, " + strings.Repeat("how are you today? ", 3)
	if len(longGreeting) <= dispatches.DefaultFastPathMaxChars {
		t.Fatalf("fixture bug: greeting is only %d chars, needs > %d", len(longGreeting), dispatches.DefaultFastPathMaxChars)
	}

	_, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage(longGreeting)},
		SessionID: "fp-ac-1-4",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := int(mockLLM.callCount.Load()); got != 1 {
		t.Errorf("intent LLM call count = %d, want 1 (intent must fire for long greeting)", got)
	}

	if !chatAgent.called.Load() {
		t.Error("chat agent should still be dispatched via normal intent flow")
	}
}

// TestFastPathIntegration_ToolTriggerRoutesToCoder verifies AC-2.1 and AC-2.2:
// a shell-like prefix such as "calc 5^6" short-circuits to the coder agent
// with zero intent LLM calls.
func TestFastPathIntegration_ToolTriggerRoutesToCoder(t *testing.T) {
	chatAgent := &callTrackingAgent{id: "chat"}
	coderAgent := &callTrackingAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("15625"),
				}, "coder"),
			},
		},
	}

	mockLLM := &sequentialMockLLM{}

	d := newFastPathIntegrationDispatcher(t, dispatches.DefaultFastPathConfig(), chatAgent, coderAgent, mockLLM)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("calc 5^6")},
		SessionID: "fp-ac-2",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := int(mockLLM.callCount.Load()); got != 0 {
		t.Errorf("intent LLM call count = %d, want 0 for tool-trigger fast-path", got)
	}

	if !coderAgent.called.Load() {
		t.Error("expected coder agent to be invoked on tool-trigger fast-path")
	}

	if chatAgent.called.Load() {
		t.Error("chat agent must not be called for a tool-trigger")
	}

	if len(resp.Messages) == 0 || resp.Messages[0].Content.Text() != "15625" {
		t.Errorf("response = %+v, want coder output", resp.Messages)
	}
}

// TestFastPathIntegration_MultiTurnDeclinesFastPath verifies AC-3.1: a request
// with 3 messages (assistant reply in history) must NOT take the fast path
// even though the last user message is "thanks". The intent LLM has to run.
func TestFastPathIntegration_MultiTurnDeclinesFastPath(t *testing.T) {
	chatAgent := &callTrackingAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("You're welcome!"),
				}, "chat"),
			},
		},
	}
	coderAgent := &callTrackingAgent{id: "coder"}

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

	d := newFastPathIntegrationDispatcher(t, dispatches.DefaultFastPathConfig(), chatAgent, coderAgent, mockLLM)

	req := &schema.RunRequest{
		Messages: []schema.Message{
			schema.NewUserMessage("write a haiku"),
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("An early sunrise / calms the restless wandering mind / code compiles at last"),
			}, "chat"),
			schema.NewUserMessage("thanks"),
		},
		SessionID: "fp-ac-3-1",
	}

	if _, err := d.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := int(mockLLM.callCount.Load()); got != 1 {
		t.Errorf("intent LLM call count = %d, want 1 (multi-turn must invoke intent)", got)
	}

	if !chatAgent.called.Load() {
		t.Error("chat agent should be dispatched via the normal intent flow")
	}
}

// TestFastPathIntegration_ToolHistoryDeclinesFastPath verifies AC-3.2: when a
// prior assistant message carries tool_calls, the fast-path must decline even
// for a matching greeting, forcing the intent LLM to run.
func TestFastPathIntegration_ToolHistoryDeclinesFastPath(t *testing.T) {
	chatAgent := &callTrackingAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Glad to help."),
				}, "chat"),
			},
		},
	}
	coderAgent := &callTrackingAgent{id: "coder"}

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"needs_exploration": false, "mode": "direct", "agent": "chat"}`),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 40, CompletionTokens: 10},
			},
		},
	}

	d := newFastPathIntegrationDispatcher(t, dispatches.DefaultFastPathConfig(), chatAgent, coderAgent, mockLLM)

	priorAssistant := schema.NewAssistantMessage(aimodel.Message{
		Role:    aimodel.RoleAssistant,
		Content: aimodel.NewTextContent(""),
		ToolCalls: []aimodel.ToolCall{{
			ID:       "call_1",
			Function: aimodel.FunctionCall{Name: "bash", Arguments: "{\"cmd\":\"ls\"}"},
		}},
	}, "coder")

	req := &schema.RunRequest{
		Messages: []schema.Message{
			priorAssistant,
			schema.NewUserMessage("thanks"),
		},
		SessionID: "fp-ac-3-2",
	}

	if _, err := d.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := int(mockLLM.callCount.Load()); got != 1 {
		t.Errorf("intent LLM call count = %d, want 1 (tool-history must invoke intent)", got)
	}

	if !chatAgent.called.Load() {
		t.Error("chat agent should be dispatched via the normal intent flow")
	}
}

// TestFastPathIntegration_DisabledFastPathRunsIntent verifies AC-4.2: passing
// WithFastPath(DisabledFastPathConfig()) turns the feature off, so even a
// "hello" greeting must go through the normal intent LLM pipeline.
func TestFastPathIntegration_DisabledFastPathRunsIntent(t *testing.T) {
	chatAgent := &callTrackingAgent{
		id: "chat",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Hi from intent path"),
				}, "chat"),
			},
		},
	}
	coderAgent := &callTrackingAgent{id: "coder"}

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			{
				Choices: []aimodel.Choice{{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"needs_exploration": false, "mode": "direct", "agent": "chat"}`),
					},
				}},
				Usage: aimodel.Usage{PromptTokens: 30, CompletionTokens: 10},
			},
		},
	}

	d := newFastPathIntegrationDispatcher(t, dispatches.DisabledFastPathConfig(), chatAgent, coderAgent, mockLLM)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "fp-ac-4-2",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := int(mockLLM.callCount.Load()); got != 1 {
		t.Errorf("intent LLM call count = %d, want 1 when fast-path disabled", got)
	}

	if !chatAgent.called.Load() {
		t.Error("chat agent should be dispatched via the normal intent flow")
	}

	if coderAgent.called.Load() {
		t.Error("coder agent must not be called for a simple greeting")
	}

	if len(resp.Messages) == 0 || resp.Messages[0].Content.Text() != "Hi from intent path" {
		t.Errorf("response = %+v, want intent-path chat output", resp.Messages)
	}
}
