package dispatches

import (
	"testing"

	"github.com/vogo/vage/schema"
)

func TestPhaseTracker_Accumulation(t *testing.T) {
	var tracker phaseTracker
	var events []schema.Event

	send := tracker.wrap(func(ev schema.Event) error {
		events = append(events, ev)
		return nil
	})

	// Send some tool call start events.
	_ = send(schema.NewEvent(schema.EventToolCallStart, "a", "s", schema.ToolCallStartData{
		ToolName: "bash", Arguments: "{}",
	}))
	_ = send(schema.NewEvent(schema.EventToolCallStart, "a", "s", schema.ToolCallStartData{
		ToolName: "read", Arguments: "{}",
	}))

	// Send an LLM call end event.
	_ = send(schema.NewEvent(schema.EventLLMCallEnd, "a", "s", schema.LLMCallEndData{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
	}))

	// Send another LLM call end event.
	_ = send(schema.NewEvent(schema.EventLLMCallEnd, "a", "s", schema.LLMCallEndData{
		PromptTokens:     2000,
		CompletionTokens: 300,
		TotalTokens:      2300,
	}))

	// Send a text delta (should be forwarded but not tracked).
	_ = send(schema.NewEvent(schema.EventTextDelta, "a", "s", schema.TextDeltaData{Delta: "hello"}))

	if tracker.toolCalls != 2 {
		t.Errorf("toolCalls = %d, want 2", tracker.toolCalls)
	}

	if tracker.promptTokens != 3000 {
		t.Errorf("promptTokens = %d, want 3000", tracker.promptTokens)
	}

	if tracker.completionTokens != 800 {
		t.Errorf("completionTokens = %d, want 800", tracker.completionTokens)
	}

	// All events should have been forwarded.
	if len(events) != 5 {
		t.Errorf("forwarded events = %d, want 5", len(events))
	}
}

func TestPhaseTracker_Empty(t *testing.T) {
	var tracker phaseTracker

	// No events sent.
	if tracker.toolCalls != 0 {
		t.Errorf("toolCalls = %d, want 0", tracker.toolCalls)
	}

	if tracker.promptTokens != 0 {
		t.Errorf("promptTokens = %d, want 0", tracker.promptTokens)
	}

	if tracker.completionTokens != 0 {
		t.Errorf("completionTokens = %d, want 0", tracker.completionTokens)
	}
}
