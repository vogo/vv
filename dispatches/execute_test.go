package dispatches

import (
	"context"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

func TestTopologicalLayers_Empty(t *testing.T) {
	layers := topologicalLayers(nil)
	if layers != nil {
		t.Errorf("topologicalLayers(nil) = %v, want nil", layers)
	}
}

func TestTopologicalLayers_SingleStep(t *testing.T) {
	steps := []PlanStep{
		{ID: "step_1", Agent: "coder", Description: "do thing"},
	}

	layers := topologicalLayers(steps)
	if len(layers) != 1 {
		t.Fatalf("layers = %d, want 1", len(layers))
	}

	if len(layers[0]) != 1 {
		t.Errorf("layer[0] = %d steps, want 1", len(layers[0]))
	}
}

func TestTopologicalLayers_Parallel(t *testing.T) {
	steps := []PlanStep{
		{ID: "step_1", Agent: "coder", Description: "task A"},
		{ID: "step_2", Agent: "researcher", Description: "task B"},
		{ID: "step_3", Agent: "reviewer", Description: "task C"},
	}

	layers := topologicalLayers(steps)
	if len(layers) != 1 {
		t.Fatalf("layers = %d, want 1 (all parallel)", len(layers))
	}

	if len(layers[0]) != 3 {
		t.Errorf("layer[0] = %d steps, want 3", len(layers[0]))
	}
}

func TestTopologicalLayers_Sequential(t *testing.T) {
	steps := []PlanStep{
		{ID: "step_1", Agent: "researcher", Description: "research"},
		{ID: "step_2", Agent: "coder", Description: "code", DependsOn: []string{"step_1"}},
		{ID: "step_3", Agent: "reviewer", Description: "review", DependsOn: []string{"step_2"}},
	}

	layers := topologicalLayers(steps)
	if len(layers) != 3 {
		t.Fatalf("layers = %d, want 3 (sequential)", len(layers))
	}

	if layers[0][0].ID != "step_1" {
		t.Errorf("layer[0][0].ID = %q, want step_1", layers[0][0].ID)
	}

	if layers[1][0].ID != "step_2" {
		t.Errorf("layer[1][0].ID = %q, want step_2", layers[1][0].ID)
	}

	if layers[2][0].ID != "step_3" {
		t.Errorf("layer[2][0].ID = %q, want step_3", layers[2][0].ID)
	}
}

func TestTopologicalLayers_Diamond(t *testing.T) {
	// A -> B, A -> C, B -> D, C -> D
	steps := []PlanStep{
		{ID: "A", Agent: "coder"},
		{ID: "B", Agent: "coder", DependsOn: []string{"A"}},
		{ID: "C", Agent: "coder", DependsOn: []string{"A"}},
		{ID: "D", Agent: "coder", DependsOn: []string{"B", "C"}},
	}

	layers := topologicalLayers(steps)
	if len(layers) != 3 {
		t.Fatalf("layers = %d, want 3 (diamond)", len(layers))
	}

	if len(layers[0]) != 1 || layers[0][0].ID != "A" {
		t.Errorf("layer[0] should contain A only")
	}

	if len(layers[1]) != 2 {
		t.Errorf("layer[1] should contain B and C, got %d", len(layers[1]))
	}

	if len(layers[2]) != 1 || layers[2][0].ID != "D" {
		t.Errorf("layer[2] should contain D only")
	}
}

func TestTopologicalLayers_Cycle(t *testing.T) {
	// A -> B, B -> A (cycle). Should not hang or panic.
	steps := []PlanStep{
		{ID: "A", Agent: "coder", DependsOn: []string{"B"}},
		{ID: "B", Agent: "coder", DependsOn: []string{"A"}},
	}

	layers := topologicalLayers(steps)
	if len(layers) == 0 {
		t.Fatal("expected at least one layer from cyclic input")
	}

	// Both steps should appear somewhere in the layers.
	total := 0
	for _, layer := range layers {
		total += len(layer)
	}

	if total != 2 {
		t.Errorf("total steps in layers = %d, want 2", total)
	}
}

func TestExecuteTask_Direct(t *testing.T) {
	reg := newTestRegistry()
	coderResp := &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("coded it"),
			}, "coder"),
		},
	}

	subAgents := map[string]agent.Agent{
		"coder":      &stubAgent{id: "coder", response: coderResp},
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	d := New(
		reg, subAgents, nil, nil, nil,
		WithFallbackAgent(&stubAgent{id: "chat"}),
	)

	intent := &IntentResult{Mode: "direct", Agent: "coder"}

	resp, _, err := d.executeTask(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("write code")},
	}, intent, "")
	if err != nil {
		t.Fatalf("executeTask: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected response messages")
	}

	if resp.Messages[0].Content.Text() != "coded it" {
		t.Errorf("response = %q, want %q", resp.Messages[0].Content.Text(), "coded it")
	}
}

func TestExecuteTask_Fallback(t *testing.T) {
	reg := newTestRegistry()
	subAgents := map[string]agent.Agent{
		"coder":      &stubAgent{id: "coder"},
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	d := New(
		reg, subAgents, nil, nil, nil,
		WithFallbackAgent(&stubAgent{id: "chat"}),
	)

	intent := &IntentResult{Mode: "unknown"}

	resp, _, err := d.executeTask(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("hello")},
	}, intent, "")
	if err != nil {
		t.Fatalf("executeTask: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected fallback response")
	}
}

func TestReplanPolicy_Defaults(t *testing.T) {
	p := DefaultReplanPolicy()

	if p.TriggerOnFailure {
		t.Error("TriggerOnFailure should default to false")
	}

	if p.TriggerOnDeviation {
		t.Error("TriggerOnDeviation should default to false")
	}

	if p.MaxReplans != 2 {
		t.Errorf("MaxReplans = %d, want 2", p.MaxReplans)
	}
}

func TestHookedAgent_RunStream(t *testing.T) {
	// Replaced statsStreamAgent (deleted with stats_integration_test.go in
	// M6 G2) with stubStreamAgent — this test only cares that the hooked
	// wrapper relays at least one event, not the exact token breakdown.
	inner := &stubStreamAgent{
		id:       "inner",
		response: "stream result",
	}

	hooked := &hookedAgent{
		inner:   inner,
		agentID: "test",
	}

	stream, err := hooked.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("test")},
		SessionID: "session",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	events := collectEvents(t, stream)
	if len(events) == 0 {
		t.Error("expected events from hooked agent stream")
	}
}

func TestHookedAgent_RunStream_NonStreamingFallback(t *testing.T) {
	inner := &stubAgent{
		id: "inner",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("non-streaming result"),
				}, "inner"),
			},
		},
	}

	hooked := &hookedAgent{
		inner:   inner,
		agentID: "test",
	}

	stream, err := hooked.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("test")},
		SessionID: "session",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	events := collectEvents(t, stream)
	if len(events) == 0 {
		t.Error("expected events from RunToStream fallback")
	}
}
