package dispatches_tests

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/dispatches"
)

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
