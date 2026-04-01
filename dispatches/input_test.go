package dispatches

import (
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/schema"
)

func TestStepInput_BuildMessages(t *testing.T) {
	input := &StepInput{
		WorkingDir:      "/tmp/project",
		ContextSummary:  "Found main.go",
		OriginalGoal:    "Set up project",
		StepDescription: "Write code",
		Upstream: map[string]StepResult{
			"step_1": {Output: "research done", Status: StepCompleted},
		},
	}

	msgs := input.BuildMessages()

	if len(msgs) < 4 {
		t.Fatalf("BuildMessages() = %d messages, want at least 4", len(msgs))
	}

	// Verify working dir is first.
	if msgs[0].Content.Text() != "Working directory: /tmp/project" {
		t.Errorf("first message = %q, want working dir", msgs[0].Content.Text())
	}
}

func TestStepInput_BuildMessages_Minimal(t *testing.T) {
	input := &StepInput{
		StepDescription: "Do something",
	}

	msgs := input.BuildMessages()

	if len(msgs) != 1 {
		t.Fatalf("BuildMessages() = %d messages, want 1", len(msgs))
	}

	if msgs[0].Content.Text() != "Do something" {
		t.Errorf("message = %q, want %q", msgs[0].Content.Text(), "Do something")
	}
}

func TestStepInput_HasFailedDependency(t *testing.T) {
	input := &StepInput{
		Upstream: map[string]StepResult{
			"step_1": {Status: StepCompleted},
			"step_2": {Status: StepFailed},
		},
	}

	if !input.HasFailedDependency() {
		t.Error("expected HasFailedDependency() = true")
	}
}

func TestStepInput_HasFailedDependency_AllCompleted(t *testing.T) {
	input := &StepInput{
		Upstream: map[string]StepResult{
			"step_1": {Status: StepCompleted},
			"step_2": {Status: StepCompleted},
		},
	}

	if input.HasFailedDependency() {
		t.Error("expected HasFailedDependency() = false")
	}
}

func TestStepInput_HasFailedDependency_Empty(t *testing.T) {
	input := &StepInput{}

	if input.HasFailedDependency() {
		t.Error("expected HasFailedDependency() = false for empty upstream")
	}
}

func TestBuildInputMapper(t *testing.T) {
	step := PlanStep{
		ID:          "step_2",
		Description: "Code it",
		Agent:       "coder",
		DependsOn:   []string{"step_1"},
	}

	mapper := BuildInputMapper("/tmp", "context", "Build a project", step, step.DependsOn, "session-1")

	upstream := map[string]*schema.RunResponse{
		"step_1": {
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("research done"),
				}, "researcher"),
			},
		},
	}

	req, err := mapper(upstream)
	if err != nil {
		t.Fatalf("mapper: %v", err)
	}

	if req.SessionID != "session-1" {
		t.Errorf("SessionID = %q, want %q", req.SessionID, "session-1")
	}

	if len(req.Messages) == 0 {
		t.Fatal("expected messages from mapper")
	}

	// Should contain at least working dir, context, goal, upstream result, and step description.
	if len(req.Messages) < 4 {
		t.Errorf("messages = %d, want at least 4", len(req.Messages))
	}
}

func TestBuildInputMapper_NoUpstream(t *testing.T) {
	step := PlanStep{
		ID:          "step_1",
		Description: "First step",
		Agent:       "coder",
	}

	mapper := BuildInputMapper("", "", "Build it", step, nil, "session-1")

	req, err := mapper(nil)
	if err != nil {
		t.Fatalf("mapper: %v", err)
	}

	// Should have goal + description.
	if len(req.Messages) != 2 {
		t.Errorf("messages = %d, want 2", len(req.Messages))
	}
}
