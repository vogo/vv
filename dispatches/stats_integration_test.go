package dispatches

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/registries"
)

// statsStreamAgent emits LLM call events and tool call events during streaming,
// allowing integration tests to verify stats accumulation without real LLM calls.
type statsStreamAgent struct {
	id               string
	response         string
	promptTokens     int
	completionTokens int
	toolCalls        int // number of tool call start events to emit
}

var _ agent.StreamAgent = (*statsStreamAgent)(nil)

func (s *statsStreamAgent) ID() string          { return s.id }
func (s *statsStreamAgent) Name() string        { return s.id }
func (s *statsStreamAgent) Description() string { return s.id }

func (s *statsStreamAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(s.response),
			}, s.id),
		},
		Usage: &aimodel.Usage{
			PromptTokens:     s.promptTokens,
			CompletionTokens: s.completionTokens,
			TotalTokens:      s.promptTokens + s.completionTokens,
		},
	}, nil
}

func (s *statsStreamAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, 16, func(_ context.Context, send func(schema.Event) error) error {
		sid := req.SessionID

		if err := send(schema.NewEvent(schema.EventAgentStart, s.id, sid, schema.AgentStartData{})); err != nil {
			return err
		}

		// Emit tool call start events.
		for range s.toolCalls {
			if err := send(schema.NewEvent(schema.EventToolCallStart, s.id, sid, schema.ToolCallStartData{
				ToolCallID: "tc-" + s.id,
				ToolName:   "bash",
				Arguments:  `{"command":"ls"}`,
			})); err != nil {
				return err
			}
		}

		// Emit LLM call end event with token counts.
		if err := send(schema.NewEvent(schema.EventLLMCallEnd, s.id, sid, schema.LLMCallEndData{
			Model:            "test-model",
			PromptTokens:     s.promptTokens,
			CompletionTokens: s.completionTokens,
			TotalTokens:      s.promptTokens + s.completionTokens,
		})); err != nil {
			return err
		}

		// Emit text delta.
		if err := send(schema.NewEvent(schema.EventTextDelta, s.id, sid, schema.TextDeltaData{Delta: s.response})); err != nil {
			return err
		}

		return send(schema.NewEvent(schema.EventAgentEnd, s.id, sid, schema.AgentEndData{
			Message: s.response,
		}))
	}), nil
}

// statsExplorerAgent is a streaming explorer that emits LLM events.
type statsExplorerAgent struct {
	id               string
	summary          string
	promptTokens     int
	completionTokens int
	toolCalls        int
}

var _ agent.StreamAgent = (*statsExplorerAgent)(nil)

func (s *statsExplorerAgent) ID() string          { return s.id }
func (s *statsExplorerAgent) Name() string        { return s.id }
func (s *statsExplorerAgent) Description() string { return s.id }

func (s *statsExplorerAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(s.summary),
			}, s.id),
		},
		Usage: &aimodel.Usage{
			PromptTokens:     s.promptTokens,
			CompletionTokens: s.completionTokens,
			TotalTokens:      s.promptTokens + s.completionTokens,
		},
	}, nil
}

func (s *statsExplorerAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, 16, func(_ context.Context, send func(schema.Event) error) error {
		sid := req.SessionID

		if err := send(schema.NewEvent(schema.EventAgentStart, s.id, sid, schema.AgentStartData{})); err != nil {
			return err
		}

		// Emit tool call starts.
		for range s.toolCalls {
			if err := send(schema.NewEvent(schema.EventToolCallStart, s.id, sid, schema.ToolCallStartData{
				ToolCallID: "tc-explore",
				ToolName:   "glob",
				Arguments:  `{"pattern":"**/*.go"}`,
			})); err != nil {
				return err
			}
		}

		// Emit LLM call end with tokens.
		if err := send(schema.NewEvent(schema.EventLLMCallEnd, s.id, sid, schema.LLMCallEndData{
			Model:            "test-model",
			PromptTokens:     s.promptTokens,
			CompletionTokens: s.completionTokens,
			TotalTokens:      s.promptTokens + s.completionTokens,
		})); err != nil {
			return err
		}

		// Emit summary text.
		if err := send(schema.NewEvent(schema.EventTextDelta, s.id, sid, schema.TextDeltaData{Delta: s.summary})); err != nil {
			return err
		}

		return send(schema.NewEvent(schema.EventAgentEnd, s.id, sid, schema.AgentEndData{
			Message: s.summary,
		}))
	}), nil
}

// statsPlannerAgent is a streaming planner that emits LLM events and returns classification JSON.
type statsPlannerAgent struct {
	id               string
	classifyJSON     string
	promptTokens     int
	completionTokens int
}

var _ agent.StreamAgent = (*statsPlannerAgent)(nil)

func (s *statsPlannerAgent) ID() string          { return s.id }
func (s *statsPlannerAgent) Name() string        { return s.id }
func (s *statsPlannerAgent) Description() string { return s.id }

func (s *statsPlannerAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(s.classifyJSON),
			}, s.id),
		},
		Usage: &aimodel.Usage{
			PromptTokens:     s.promptTokens,
			CompletionTokens: s.completionTokens,
			TotalTokens:      s.promptTokens + s.completionTokens,
		},
	}, nil
}

func (s *statsPlannerAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, 16, func(_ context.Context, send func(schema.Event) error) error {
		sid := req.SessionID

		if err := send(schema.NewEvent(schema.EventAgentStart, s.id, sid, schema.AgentStartData{})); err != nil {
			return err
		}

		// Emit LLM call end.
		if err := send(schema.NewEvent(schema.EventLLMCallEnd, s.id, sid, schema.LLMCallEndData{
			Model:            "test-model",
			PromptTokens:     s.promptTokens,
			CompletionTokens: s.completionTokens,
			TotalTokens:      s.promptTokens + s.completionTokens,
		})); err != nil {
			return err
		}

		// Emit classification JSON as text.
		if err := send(schema.NewEvent(schema.EventTextDelta, s.id, sid, schema.TextDeltaData{Delta: s.classifyJSON})); err != nil {
			return err
		}

		return send(schema.NewEvent(schema.EventAgentEnd, s.id, sid, schema.AgentEndData{
			Message: s.classifyJSON,
		}))
	}), nil
}

// collectEvents drains a RunStream and returns all events.
func collectEvents(t *testing.T, stream *schema.RunStream) []schema.Event {
	t.Helper()

	var events []schema.Event

	for {
		event, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			t.Fatalf("Recv: %v", err)
		}

		events = append(events, event)
	}

	return events
}

// filterEvents returns events of the given type.
func filterEvents(events []schema.Event, eventType string) []schema.Event {
	var filtered []schema.Event

	for _, ev := range events {
		if ev.Type == eventType {
			filtered = append(filtered, ev)
		}
	}

	return filtered
}

// TestRunStream_PhaseEndContainsStats verifies that PhaseEndData events emitted
// by the dispatcher carry correct token and tool call stats.
func TestRunStream_PhaseEndContainsStats(t *testing.T) {
	reg := registries.New()
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		reg.MustRegister(registries.AgentDescriptor{
			ID: id, DisplayName: id, Description: id, Dispatchable: true,
		})
	}

	explorer := &statsExplorerAgent{
		id:               "explorer",
		summary:          "Found main.go",
		promptTokens:     500,
		completionTokens: 200,
		toolCalls:        3,
	}

	planner := &statsPlannerAgent{
		id:               "planner",
		classifyJSON:     `{"mode": "direct", "agent": "coder"}`,
		promptTokens:     300,
		completionTokens: 100,
	}

	coder := &statsStreamAgent{
		id:               "coder",
		response:         "done coding",
		promptTokens:     2000,
		completionTokens: 800,
		toolCalls:        5,
	}

	subAgents := map[string]agent.Agent{
		"coder":      coder,
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	d := New(
		reg, subAgents, explorer, planner, nil,
		WithLLM(&mockChatCompleter{}, "test-model"),
		WithFallbackAgent(&stubAgent{id: "chat"}),
		WithWorkingDir("/tmp/test"),
	)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("write code")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	events := collectEvents(t, stream)

	// Verify we got phase end events for all three phases.
	phaseEnds := filterEvents(events, schema.EventPhaseEnd)
	if len(phaseEnds) != 3 {
		t.Fatalf("expected 3 PhaseEnd events, got %d", len(phaseEnds))
	}

	// Explore phase.
	exploreEnd, ok := phaseEnds[0].Data.(schema.PhaseEndData)
	if !ok {
		t.Fatal("PhaseEnd[0] data is not PhaseEndData")
	}

	if exploreEnd.Phase != "explore" {
		t.Errorf("PhaseEnd[0].Phase = %q, want %q", exploreEnd.Phase, "explore")
	}

	if exploreEnd.PromptTokens != 500 {
		t.Errorf("explore PromptTokens = %d, want 500", exploreEnd.PromptTokens)
	}

	if exploreEnd.CompletionTokens != 200 {
		t.Errorf("explore CompletionTokens = %d, want 200", exploreEnd.CompletionTokens)
	}

	if exploreEnd.ToolCalls != 3 {
		t.Errorf("explore ToolCalls = %d, want 3", exploreEnd.ToolCalls)
	}

	if exploreEnd.Duration < 0 {
		t.Errorf("explore Duration = %d, want >= 0", exploreEnd.Duration)
	}

	// Plan phase.
	planEnd, ok := phaseEnds[1].Data.(schema.PhaseEndData)
	if !ok {
		t.Fatal("PhaseEnd[1] data is not PhaseEndData")
	}

	if planEnd.Phase != "plan" {
		t.Errorf("PhaseEnd[1].Phase = %q, want %q", planEnd.Phase, "plan")
	}

	if planEnd.PromptTokens != 300 {
		t.Errorf("plan PromptTokens = %d, want 300", planEnd.PromptTokens)
	}

	if planEnd.CompletionTokens != 100 {
		t.Errorf("plan CompletionTokens = %d, want 100", planEnd.CompletionTokens)
	}

	// Dispatch phase.
	dispatchEnd, ok := phaseEnds[2].Data.(schema.PhaseEndData)
	if !ok {
		t.Fatal("PhaseEnd[2] data is not PhaseEndData")
	}

	if dispatchEnd.Phase != "dispatch" {
		t.Errorf("PhaseEnd[2].Phase = %q, want %q", dispatchEnd.Phase, "dispatch")
	}

	if dispatchEnd.PromptTokens != 2000 {
		t.Errorf("dispatch PromptTokens = %d, want 2000", dispatchEnd.PromptTokens)
	}

	if dispatchEnd.CompletionTokens != 800 {
		t.Errorf("dispatch CompletionTokens = %d, want 800", dispatchEnd.CompletionTokens)
	}

	if dispatchEnd.ToolCalls != 5 {
		t.Errorf("dispatch ToolCalls = %d, want 5", dispatchEnd.ToolCalls)
	}
}

// TestRunStream_SubAgentEndContainsTokenBreakdown verifies that SubAgentEndData
// events carry separate prompt and completion token counts and that TokensUsed
// equals their sum.
func TestRunStream_SubAgentEndContainsTokenBreakdown(t *testing.T) {
	reg := registries.New()
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		reg.MustRegister(registries.AgentDescriptor{
			ID: id, DisplayName: id, Description: id, Dispatchable: true,
		})
	}

	planner := &statsPlannerAgent{
		id:               "planner",
		classifyJSON:     `{"mode": "direct", "agent": "researcher"}`,
		promptTokens:     100,
		completionTokens: 50,
	}

	researcher := &statsStreamAgent{
		id:               "researcher",
		response:         "research done",
		promptTokens:     3000,
		completionTokens: 1200,
		toolCalls:        2,
	}

	subAgents := map[string]agent.Agent{
		"coder":      &stubAgent{id: "coder"},
		"researcher": researcher,
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	d := New(
		reg, subAgents, nil, planner, nil,
		WithLLM(&mockChatCompleter{}, "test-model"),
		WithFallbackAgent(&stubAgent{id: "chat"}),
	)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("research something")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	events := collectEvents(t, stream)

	subAgentEnds := filterEvents(events, schema.EventSubAgentEnd)
	if len(subAgentEnds) == 0 {
		t.Fatal("expected at least one SubAgentEnd event")
	}

	// Find the researcher sub-agent end.
	var found bool

	for _, ev := range subAgentEnds {
		data, ok := ev.Data.(schema.SubAgentEndData)
		if !ok {
			continue
		}

		if data.AgentName != "researcher" {
			continue
		}

		found = true

		if data.PromptTokens != 3000 {
			t.Errorf("SubAgentEnd PromptTokens = %d, want 3000", data.PromptTokens)
		}

		if data.CompletionTokens != 1200 {
			t.Errorf("SubAgentEnd CompletionTokens = %d, want 1200", data.CompletionTokens)
		}

		if data.TokensUsed != 4200 {
			t.Errorf("SubAgentEnd TokensUsed = %d, want 4200 (sum)", data.TokensUsed)
		}

		if data.ToolCalls != 2 {
			t.Errorf("SubAgentEnd ToolCalls = %d, want 2", data.ToolCalls)
		}

		if data.Duration < 0 {
			t.Errorf("SubAgentEnd Duration = %d, want >= 0", data.Duration)
		}
	}

	if !found {
		t.Error("did not find SubAgentEnd event for researcher")
	}
}

// TestRunStream_StatsConsistency verifies that the sum of PromptTokens from all
// PhaseEndData events matches the sum from all EventLLMCallEnd events in the stream.
func TestRunStream_StatsConsistency(t *testing.T) {
	reg := registries.New()
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		reg.MustRegister(registries.AgentDescriptor{
			ID: id, DisplayName: id, Description: id, Dispatchable: true,
		})
	}

	explorer := &statsExplorerAgent{
		id:               "explorer",
		summary:          "project context",
		promptTokens:     400,
		completionTokens: 150,
		toolCalls:        2,
	}

	planner := &statsPlannerAgent{
		id:               "planner",
		classifyJSON:     `{"mode": "direct", "agent": "coder"}`,
		promptTokens:     200,
		completionTokens: 80,
	}

	coder := &statsStreamAgent{
		id:               "coder",
		response:         "code written",
		promptTokens:     1500,
		completionTokens: 600,
		toolCalls:        4,
	}

	subAgents := map[string]agent.Agent{
		"coder":      coder,
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	d := New(
		reg, subAgents, explorer, planner, nil,
		WithLLM(&mockChatCompleter{}, "test-model"),
		WithFallbackAgent(&stubAgent{id: "chat"}),
		WithWorkingDir("/tmp/test"),
	)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("write code")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	events := collectEvents(t, stream)

	// Sum PromptTokens from PhaseEndData.
	var phasePromptTotal, phaseCompletionTotal int

	for _, ev := range filterEvents(events, schema.EventPhaseEnd) {
		if data, ok := ev.Data.(schema.PhaseEndData); ok {
			phasePromptTotal += data.PromptTokens
			phaseCompletionTotal += data.CompletionTokens
		}
	}

	// Sum PromptTokens from LLMCallEndData.
	var llmPromptTotal, llmCompletionTotal int

	for _, ev := range filterEvents(events, schema.EventLLMCallEnd) {
		if data, ok := ev.Data.(schema.LLMCallEndData); ok {
			llmPromptTotal += data.PromptTokens
			llmCompletionTotal += data.CompletionTokens
		}
	}

	if phasePromptTotal != llmPromptTotal {
		t.Errorf("phase PromptTokens total (%d) != LLM PromptTokens total (%d)", phasePromptTotal, llmPromptTotal)
	}

	if phaseCompletionTotal != llmCompletionTotal {
		t.Errorf("phase CompletionTokens total (%d) != LLM CompletionTokens total (%d)", phaseCompletionTotal, llmCompletionTotal)
	}

	// Verify expected totals.
	expectedPrompt := 400 + 200 + 1500
	if phasePromptTotal != expectedPrompt {
		t.Errorf("total PromptTokens = %d, want %d", phasePromptTotal, expectedPrompt)
	}

	expectedCompletion := 150 + 80 + 600
	if phaseCompletionTotal != expectedCompletion {
		t.Errorf("total CompletionTokens = %d, want %d", phaseCompletionTotal, expectedCompletion)
	}
}

// TestRunStream_NonStreamingSubAgentStats verifies that stats are correctly
// populated when the sub-agent uses the non-streaming (Run) fallback path.
func TestRunStream_NonStreamingSubAgentStats(t *testing.T) {
	reg := registries.New()
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		reg.MustRegister(registries.AgentDescriptor{
			ID: id, DisplayName: id, Description: id, Dispatchable: true,
		})
	}

	// Use a non-streaming stubAgent (implements Agent but not StreamAgent).
	coderStub := &stubAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("non-streaming response"),
				}, "coder"),
			},
			Usage: &aimodel.Usage{
				PromptTokens:     1000,
				CompletionTokens: 400,
				TotalTokens:      1400,
			},
		},
	}

	planner := &statsPlannerAgent{
		id:               "planner",
		classifyJSON:     `{"mode": "direct", "agent": "coder"}`,
		promptTokens:     100,
		completionTokens: 50,
	}

	subAgents := map[string]agent.Agent{
		"coder":      coderStub,
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	d := New(
		reg, subAgents, nil, planner, nil,
		WithLLM(&mockChatCompleter{}, "test-model"),
		WithFallbackAgent(&stubAgent{id: "chat"}),
	)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("test")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	events := collectEvents(t, stream)

	// Find SubAgentEnd for coder.
	subAgentEnds := filterEvents(events, schema.EventSubAgentEnd)

	var found bool

	for _, ev := range subAgentEnds {
		data, ok := ev.Data.(schema.SubAgentEndData)
		if !ok || data.AgentName != "coder" {
			continue
		}

		found = true

		if data.PromptTokens != 1000 {
			t.Errorf("non-streaming SubAgentEnd PromptTokens = %d, want 1000", data.PromptTokens)
		}

		if data.CompletionTokens != 400 {
			t.Errorf("non-streaming SubAgentEnd CompletionTokens = %d, want 400", data.CompletionTokens)
		}

		if data.TokensUsed != 1400 {
			t.Errorf("non-streaming SubAgentEnd TokensUsed = %d, want 1400", data.TokensUsed)
		}
	}

	if !found {
		t.Error("did not find SubAgentEnd event for coder (non-streaming)")
	}
}

// TestRunStream_ZeroStatsPhase verifies that phases with no LLM calls or tool
// calls still emit PhaseEndData with zero token and tool counts.
func TestRunStream_ZeroStatsPhase(t *testing.T) {
	reg := registries.New()
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		reg.MustRegister(registries.AgentDescriptor{
			ID: id, DisplayName: id, Description: id, Dispatchable: true,
		})
	}

	// Planner that emits no LLM events (non-streaming fallback).
	plannerStub := &stubAgent{
		id: "planner",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent(`{"mode": "direct", "agent": "chat"}`),
				}, "planner"),
			},
		},
	}

	chatStub := &stubAgent{id: "chat"}

	subAgents := map[string]agent.Agent{
		"coder":      &stubAgent{id: "coder"},
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       chatStub,
	}

	d := New(
		reg, subAgents, nil, plannerStub, nil,
		WithLLM(&mockChatCompleter{}, "test-model"),
		WithFallbackAgent(chatStub),
	)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	events := collectEvents(t, stream)

	phaseEnds := filterEvents(events, schema.EventPhaseEnd)
	if len(phaseEnds) < 2 {
		t.Fatalf("expected at least 2 PhaseEnd events (plan + dispatch), got %d", len(phaseEnds))
	}

	// Plan phase should have zero stats because the planner is non-streaming.
	planEnd, ok := phaseEnds[0].Data.(schema.PhaseEndData)
	if !ok {
		t.Fatal("PhaseEnd[0] data is not PhaseEndData")
	}

	if planEnd.Phase != "plan" {
		t.Errorf("PhaseEnd[0].Phase = %q, want %q", planEnd.Phase, "plan")
	}

	if planEnd.PromptTokens != 0 {
		t.Errorf("plan PromptTokens = %d, want 0", planEnd.PromptTokens)
	}

	if planEnd.CompletionTokens != 0 {
		t.Errorf("plan CompletionTokens = %d, want 0", planEnd.CompletionTokens)
	}

	if planEnd.ToolCalls != 0 {
		t.Errorf("plan ToolCalls = %d, want 0", planEnd.ToolCalls)
	}

	// Duration should still be non-negative.
	if planEnd.Duration < 0 {
		t.Errorf("plan Duration = %d, want >= 0", planEnd.Duration)
	}
}

// TestRunStream_LargeTokenCounts verifies that large token counts are accumulated
// correctly without overflow for realistic large-context model scenarios.
func TestRunStream_LargeTokenCounts(t *testing.T) {
	reg := registries.New()
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		reg.MustRegister(registries.AgentDescriptor{
			ID: id, DisplayName: id, Description: id, Dispatchable: true,
		})
	}

	planner := &statsPlannerAgent{
		id:               "planner",
		classifyJSON:     `{"mode": "direct", "agent": "coder"}`,
		promptTokens:     500000, // 500k prompt tokens
		completionTokens: 100000, // 100k completion tokens
	}

	coder := &statsStreamAgent{
		id:               "coder",
		response:         "large context response",
		promptTokens:     1200000, // 1.2M prompt tokens
		completionTokens: 50000,   // 50k completion tokens
		toolCalls:        100,
	}

	subAgents := map[string]agent.Agent{
		"coder":      coder,
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	d := New(
		reg, subAgents, nil, planner, nil,
		WithLLM(&mockChatCompleter{}, "test-model"),
		WithFallbackAgent(&stubAgent{id: "chat"}),
	)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("complex task")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	events := collectEvents(t, stream)

	phaseEnds := filterEvents(events, schema.EventPhaseEnd)

	// Sum all phase stats.
	var totalPrompt, totalCompletion, totalToolCalls int

	for _, ev := range phaseEnds {
		if data, ok := ev.Data.(schema.PhaseEndData); ok {
			totalPrompt += data.PromptTokens
			totalCompletion += data.CompletionTokens
			totalToolCalls += data.ToolCalls
		}
	}

	expectedPrompt := 500000 + 1200000
	if totalPrompt != expectedPrompt {
		t.Errorf("total PromptTokens = %d, want %d", totalPrompt, expectedPrompt)
	}

	expectedCompletion := 100000 + 50000
	if totalCompletion != expectedCompletion {
		t.Errorf("total CompletionTokens = %d, want %d", totalCompletion, expectedCompletion)
	}

	if totalToolCalls != 100 {
		t.Errorf("total ToolCalls = %d, want 100", totalToolCalls)
	}
}

// TestRunStream_PhaseStartAndEndPairing verifies that every PhaseStart event
// has a corresponding PhaseEnd event and phases appear in the expected order.
func TestRunStream_PhaseStartAndEndPairing(t *testing.T) {
	reg := registries.New()
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		reg.MustRegister(registries.AgentDescriptor{
			ID: id, DisplayName: id, Description: id, Dispatchable: true,
		})
	}

	explorer := &statsExplorerAgent{
		id:               "explorer",
		summary:          "context",
		promptTokens:     100,
		completionTokens: 50,
	}

	planner := &statsPlannerAgent{
		id:               "planner",
		classifyJSON:     `{"mode": "direct", "agent": "coder"}`,
		promptTokens:     100,
		completionTokens: 50,
	}

	coder := &statsStreamAgent{
		id:               "coder",
		response:         "done",
		promptTokens:     100,
		completionTokens: 50,
	}

	subAgents := map[string]agent.Agent{
		"coder":      coder,
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	d := New(
		reg, subAgents, explorer, planner, nil,
		WithLLM(&mockChatCompleter{}, "test-model"),
		WithFallbackAgent(&stubAgent{id: "chat"}),
		WithWorkingDir("/tmp/test"),
	)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("test")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	events := collectEvents(t, stream)

	phaseStarts := filterEvents(events, schema.EventPhaseStart)
	phaseEnds := filterEvents(events, schema.EventPhaseEnd)

	if len(phaseStarts) != 3 {
		t.Fatalf("expected 3 PhaseStart events, got %d", len(phaseStarts))
	}

	if len(phaseEnds) != 3 {
		t.Fatalf("expected 3 PhaseEnd events, got %d", len(phaseEnds))
	}

	expectedPhases := []string{"explore", "plan", "dispatch"}

	for i, phase := range expectedPhases {
		startData, ok := phaseStarts[i].Data.(schema.PhaseStartData)
		if !ok {
			t.Fatalf("PhaseStart[%d] data is not PhaseStartData", i)
		}

		if startData.Phase != phase {
			t.Errorf("PhaseStart[%d].Phase = %q, want %q", i, startData.Phase, phase)
		}

		endData, ok := phaseEnds[i].Data.(schema.PhaseEndData)
		if !ok {
			t.Fatalf("PhaseEnd[%d] data is not PhaseEndData", i)
		}

		if endData.Phase != phase {
			t.Errorf("PhaseEnd[%d].Phase = %q, want %q", i, endData.Phase, phase)
		}
	}

	// Verify ordering: each PhaseStart should appear before its PhaseEnd.
	phaseStartIdx := make(map[string]int)
	phaseEndIdx := make(map[string]int)

	for i, ev := range events {
		switch ev.Type {
		case schema.EventPhaseStart:
			if data, ok := ev.Data.(schema.PhaseStartData); ok {
				phaseStartIdx[data.Phase] = i
			}
		case schema.EventPhaseEnd:
			if data, ok := ev.Data.(schema.PhaseEndData); ok {
				phaseEndIdx[data.Phase] = i
			}
		}
	}

	for _, phase := range expectedPhases {
		startIdx, ok1 := phaseStartIdx[phase]
		endIdx, ok2 := phaseEndIdx[phase]

		if !ok1 || !ok2 {
			t.Errorf("missing start or end index for phase %q", phase)
			continue
		}

		if startIdx >= endIdx {
			t.Errorf("PhaseStart for %q (idx %d) should come before PhaseEnd (idx %d)", phase, startIdx, endIdx)
		}
	}
}
