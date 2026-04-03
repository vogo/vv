package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
)

// TestModelAccumulatesTaskStats verifies that the CLI model correctly accumulates
// task-level stats from EventLLMCallEnd events across the lifetime of a request.
func TestModelAccumulatesTaskStats(t *testing.T) {
	orchestrator := &stubStreamAgent{id: "orchestrator", response: "ok"}
	app := New(orchestrator, &configs.Config{}, nil, nil, nil)
	m := newModel(app, context.Background())

	// Simulate task start.
	m.taskStart = time.Now()
	m.totalPromptTokens = 0
	m.totalCompletionTokens = 0
	m.totalToolCalls = 0

	// Simulate multiple LLM call end events (as would come from different phases).
	llmEvents := []schema.LLMCallEndData{
		{PromptTokens: 500, CompletionTokens: 200, TotalTokens: 700},
		{PromptTokens: 300, CompletionTokens: 100, TotalTokens: 400},
		{PromptTokens: 2000, CompletionTokens: 800, TotalTokens: 2800},
	}

	for _, llmData := range llmEvents {
		event := schema.NewEvent(schema.EventLLMCallEnd, "test", "sess", llmData)
		m.handleStreamEvent(streamEventMsg{event: event})
	}

	// Verify task-level accumulation.
	if m.totalPromptTokens != 2800 {
		t.Errorf("totalPromptTokens = %d, want 2800", m.totalPromptTokens)
	}

	if m.totalCompletionTokens != 1100 {
		t.Errorf("totalCompletionTokens = %d, want 1100", m.totalCompletionTokens)
	}
}

// TestModelAccumulatesToolCalls verifies that tool calls are counted at both
// the sub-agent and task level.
func TestModelAccumulatesToolCalls(t *testing.T) {
	orchestrator := &stubStreamAgent{id: "orchestrator", response: "ok"}
	app := New(orchestrator, &configs.Config{}, nil, nil, nil)
	m := newModel(app, context.Background())

	m.taskStart = time.Now()
	m.totalToolCalls = 0
	m.toolCallCount = 0

	// Simulate tool call start events.
	for range 5 {
		event := schema.NewEvent(schema.EventToolCallStart, "test", "sess", schema.ToolCallStartData{
			ToolCallID: "tc",
			ToolName:   "bash",
			Arguments:  `{"command":"ls"}`,
		})
		m.handleStreamEvent(streamEventMsg{event: event})
	}

	if m.totalToolCalls != 5 {
		t.Errorf("totalToolCalls = %d, want 5", m.totalToolCalls)
	}

	if m.toolCallCount != 5 {
		t.Errorf("toolCallCount = %d, want 5", m.toolCallCount)
	}
}

// TestModelSubAgentStatsReset verifies that sub-agent level stats accumulators
// are reset when a new sub-agent starts and after a sub-agent ends.
func TestModelSubAgentStatsReset(t *testing.T) {
	orchestrator := &stubStreamAgent{id: "orchestrator", response: "ok"}
	app := New(orchestrator, &configs.Config{}, nil, nil, nil)
	m := newModel(app, context.Background())

	m.taskStart = time.Now()

	// Start first sub-agent.
	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventSubAgentStart, "test", "sess", schema.SubAgentStartData{AgentName: "coder"},
	)})

	if m.subAgentPromptTokens != 0 {
		t.Errorf("after SubAgentStart, subAgentPromptTokens = %d, want 0", m.subAgentPromptTokens)
	}

	// Accumulate some LLM tokens within the sub-agent.
	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventLLMCallEnd, "test", "sess", schema.LLMCallEndData{
			PromptTokens: 1000, CompletionTokens: 500,
		},
	)})

	if m.subAgentPromptTokens != 1000 {
		t.Errorf("subAgentPromptTokens = %d, want 1000", m.subAgentPromptTokens)
	}

	if m.subAgentCompletionTokens != 500 {
		t.Errorf("subAgentCompletionTokens = %d, want 500", m.subAgentCompletionTokens)
	}

	// End the sub-agent.
	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventSubAgentEnd, "test", "sess", schema.SubAgentEndData{
			AgentName: "coder", Duration: 5000,
		},
	)})

	// Accumulators should be reset.
	if m.subAgentPromptTokens != 0 {
		t.Errorf("after SubAgentEnd, subAgentPromptTokens = %d, want 0", m.subAgentPromptTokens)
	}

	if m.subAgentCompletionTokens != 0 {
		t.Errorf("after SubAgentEnd, subAgentCompletionTokens = %d, want 0", m.subAgentCompletionTokens)
	}

	if m.toolCallCount != 0 {
		t.Errorf("after SubAgentEnd, toolCallCount = %d, want 0", m.toolCallCount)
	}

	// Task-level should still be accumulated.
	if m.totalPromptTokens != 1000 {
		t.Errorf("totalPromptTokens = %d, want 1000 (should persist across sub-agents)", m.totalPromptTokens)
	}
}

// TestModelSubAgentFallbackStats verifies that the CLI model falls back to
// locally tracked stats when SubAgentEndData has zero values (DAG path).
func TestModelSubAgentFallbackStats(t *testing.T) {
	orchestrator := &stubStreamAgent{id: "orchestrator", response: "ok"}
	app := New(orchestrator, &configs.Config{}, nil, nil, nil)
	m := newModel(app, context.Background())

	m.taskStart = time.Now()

	// Start sub-agent.
	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventSubAgentStart, "test", "sess", schema.SubAgentStartData{AgentName: "researcher"},
	)})

	// Simulate LLM events and tool calls accumulating in the CLI model.
	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventLLMCallEnd, "test", "sess", schema.LLMCallEndData{
			PromptTokens: 750, CompletionTokens: 300,
		},
	)})

	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventToolCallStart, "test", "sess", schema.ToolCallStartData{
			ToolCallID: "tc", ToolName: "bash", Arguments: "{}",
		},
	)})

	// End sub-agent with zero stats (simulating DAG path).
	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventSubAgentEnd, "test", "sess", schema.SubAgentEndData{
			AgentName: "researcher",
			Duration:  3000,
			// All token/tool fields are zero -- DAG path.
		},
	)})

	// The model should have used the locally tracked fallback stats.
	// We cannot directly inspect the rendered output here, but we verify
	// that the accumulators were consulted and reset.
	if m.subAgentPromptTokens != 0 {
		t.Errorf("subAgentPromptTokens not reset after SubAgentEnd fallback: %d", m.subAgentPromptTokens)
	}

	if m.subAgentCompletionTokens != 0 {
		t.Errorf("subAgentCompletionTokens not reset after SubAgentEnd fallback: %d", m.subAgentCompletionTokens)
	}
}

// TestPhaseEndRendering verifies that the CLI model constructs correct render
// calls when receiving PhaseEndData with stats.
func TestPhaseEndRendering(t *testing.T) {
	// Test the render function directly with various PhaseEndData scenarios.
	tests := []struct {
		name     string
		phase    string
		stats    execStats
		contains []string
		excludes []string
	}{
		{
			name:  "explore with all stats",
			phase: "explore",
			stats: execStats{
				ToolCalls:        3,
				DurationMs:       5000,
				PromptTokens:     2000,
				CompletionTokens: 800,
			},
			contains: []string{"Explore", "complete.", "3 tool uses", "5s", "\u2191 2.0k", "\u2193 800"},
		},
		{
			name:  "plan with no tool calls",
			phase: "plan",
			stats: execStats{
				DurationMs:       1500,
				PromptTokens:     500,
				CompletionTokens: 100,
			},
			contains: []string{"Plan", "complete.", "1s", "\u2191 500", "\u2193 100"},
			excludes: []string{"tool"},
		},
		{
			name:  "dispatch with zero tokens (DAG path)",
			phase: "dispatch",
			stats: execStats{
				DurationMs: 30000,
			},
			contains: []string{"Dispatch", "complete.", "30s"},
			excludes: []string{"\u2191", "\u2193", "tool"},
		},
		{
			name:  "large token values",
			phase: "dispatch",
			stats: execStats{
				ToolCalls:        50,
				DurationMs:       120000,
				PromptTokens:     1500000,
				CompletionTokens: 500000,
			},
			contains: []string{"Dispatch", "complete.", "50 tool uses", "2m", "\u2191 1.5M", "\u2193 500.0k"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderPhaseTransition(tt.phase, false, tt.stats, 0)

			for _, want := range tt.contains {
				if !strings.Contains(result, want) {
					t.Errorf("renderPhaseTransition(%q) missing %q, got %q", tt.phase, want, result)
				}
			}

			for _, dontWant := range tt.excludes {
				if strings.Contains(result, dontWant) {
					t.Errorf("renderPhaseTransition(%q) should not contain %q, got %q", tt.phase, dontWant, result)
				}
			}
		})
	}
}

// TestTaskCompleteRendering verifies the task completion line rendering with
// various stat combinations.
func TestTaskCompleteRendering(t *testing.T) {
	tests := []struct {
		name     string
		stats    execStats
		contains []string
		excludes []string
	}{
		{
			name: "full stats",
			stats: execStats{
				DurationMs:       45000,
				PromptTokens:     10000,
				CompletionTokens: 3000,
			},
			contains: []string{"task complete.", "45s", "\u2191 10.0k", "\u2193 3.0k"},
		},
		{
			name:     "duration only",
			stats:    execStats{DurationMs: 2000},
			contains: []string{"task complete.", "2s"},
			excludes: []string{"\u2191", "\u2193"},
		},
		{
			name: "sub-second duration",
			stats: execStats{
				DurationMs:       500,
				PromptTokens:     100,
				CompletionTokens: 50,
			},
			contains: []string{"task complete.", "500ms", "\u2191 100", "\u2193 50"},
		},
		{
			name: "minute-plus duration",
			stats: execStats{
				DurationMs:       125000,
				PromptTokens:     50000,
				CompletionTokens: 20000,
			},
			contains: []string{"task complete.", "2m 5s", "\u2191 50.0k", "\u2193 20.0k"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderTaskComplete(tt.stats)

			for _, want := range tt.contains {
				if !strings.Contains(result, want) {
					t.Errorf("renderTaskComplete missing %q, got %q", want, result)
				}
			}

			for _, dontWant := range tt.excludes {
				if strings.Contains(result, dontWant) {
					t.Errorf("renderTaskComplete should not contain %q, got %q", dontWant, result)
				}
			}
		})
	}
}

// TestMultiSubAgentAccumulation verifies that task-level stats accumulate
// correctly across multiple sequential sub-agents.
func TestMultiSubAgentAccumulation(t *testing.T) {
	orchestrator := &stubStreamAgent{id: "orchestrator", response: "ok"}
	app := New(orchestrator, &configs.Config{}, nil, nil, nil)
	m := newModel(app, context.Background())

	m.taskStart = time.Now()

	// Sub-agent 1: researcher.
	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventSubAgentStart, "test", "sess", schema.SubAgentStartData{AgentName: "researcher"},
	)})

	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventLLMCallEnd, "test", "sess", schema.LLMCallEndData{
			PromptTokens: 1000, CompletionTokens: 400,
		},
	)})

	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventToolCallStart, "test", "sess", schema.ToolCallStartData{
			ToolCallID: "tc1", ToolName: "grep", Arguments: "{}",
		},
	)})
	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventToolCallStart, "test", "sess", schema.ToolCallStartData{
			ToolCallID: "tc2", ToolName: "read", Arguments: "{}",
		},
	)})

	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventSubAgentEnd, "test", "sess", schema.SubAgentEndData{
			AgentName:        "researcher",
			Duration:         5000,
			ToolCalls:        2,
			PromptTokens:     1000,
			CompletionTokens: 400,
			TokensUsed:       1400,
		},
	)})

	// Sub-agent 2: coder.
	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventSubAgentStart, "test", "sess", schema.SubAgentStartData{AgentName: "coder"},
	)})

	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventLLMCallEnd, "test", "sess", schema.LLMCallEndData{
			PromptTokens: 3000, CompletionTokens: 1200,
		},
	)})

	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventToolCallStart, "test", "sess", schema.ToolCallStartData{
			ToolCallID: "tc3", ToolName: "write", Arguments: "{}",
		},
	)})

	m.handleStreamEvent(streamEventMsg{event: schema.NewEvent(
		schema.EventSubAgentEnd, "test", "sess", schema.SubAgentEndData{
			AgentName:        "coder",
			Duration:         10000,
			ToolCalls:        1,
			PromptTokens:     3000,
			CompletionTokens: 1200,
			TokensUsed:       4200,
		},
	)})

	// Verify task-level totals span both sub-agents.
	if m.totalPromptTokens != 4000 {
		t.Errorf("totalPromptTokens = %d, want 4000", m.totalPromptTokens)
	}

	if m.totalCompletionTokens != 1600 {
		t.Errorf("totalCompletionTokens = %d, want 1600", m.totalCompletionTokens)
	}

	if m.totalToolCalls != 3 {
		t.Errorf("totalToolCalls = %d, want 3", m.totalToolCalls)
	}
}

// TestFormatDuration_EdgeCases tests duration formatting at boundary values.
func TestFormatDuration_EdgeCases(t *testing.T) {
	tests := []struct {
		ms   int64
		want string
	}{
		{0, "0ms"},
		{1, "1ms"},
		{999, "999ms"},
		{1000, "1s"},
		{59000, "59s"},
		{60000, "1m"},
		{61000, "1m 1s"},
		{3600000, "60m"},
		{3661000, "61m 1s"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.ms)
		if got != tt.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

// TestBuildStatsLine_AllCombinations tests various combinations of stats fields.
func TestBuildStatsLine_AllCombinations(t *testing.T) {
	tests := []struct {
		name     string
		stats    execStats
		contains []string
		excludes []string
	}{
		{
			name:     "only duration (zero ms)",
			stats:    execStats{DurationMs: 0},
			contains: []string{"(0ms)"},
		},
		{
			name:     "tools + duration, no tokens",
			stats:    execStats{ToolCalls: 10, DurationMs: 5000},
			contains: []string{"10 tool uses", "5s"},
			excludes: []string{"\u2191", "\u2193"},
		},
		{
			name:     "only prompt tokens, no completion",
			stats:    execStats{DurationMs: 1000, PromptTokens: 500},
			contains: []string{"1s", "\u2191 500"},
			excludes: []string{"\u2193"},
		},
		{
			name:     "only completion tokens, no prompt",
			stats:    execStats{DurationMs: 1000, CompletionTokens: 200},
			contains: []string{"1s", "\u2193 200"},
			excludes: []string{"\u2191"},
		},
		{
			name:     "1 tool use singular",
			stats:    execStats{ToolCalls: 1, DurationMs: 500},
			contains: []string{"1 tool use"},
			excludes: []string{"tool uses"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildStatsLine(tt.stats)

			for _, want := range tt.contains {
				if !strings.Contains(result, want) {
					t.Errorf("buildStatsLine missing %q, got %q", want, result)
				}
			}

			for _, dontWant := range tt.excludes {
				if strings.Contains(result, dontWant) {
					t.Errorf("buildStatsLine should not contain %q, got %q", dontWant, result)
				}
			}
		})
	}
}
