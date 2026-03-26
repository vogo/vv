package agents

import (
	"strings"
	"testing"
)

func TestCoderSystemPrompt_NotEmpty(t *testing.T) {
	if CoderSystemPrompt == "" {
		t.Fatal("CoderSystemPrompt is empty")
	}
}

func TestCoderSystemPrompt_ContainsToolNames(t *testing.T) {
	tools := []string{"bash", "read", "write", "edit", "glob", "grep"}
	for _, tool := range tools {
		if !strings.Contains(CoderSystemPrompt, tool) {
			t.Errorf("CoderSystemPrompt does not mention tool %q", tool)
		}
	}
}

func TestChatSystemPrompt_NotEmpty(t *testing.T) {
	if ChatSystemPrompt == "" {
		t.Fatal("ChatSystemPrompt is empty")
	}
}

func TestChatSystemPrompt_ContainsGuidelines(t *testing.T) {
	if !strings.Contains(ChatSystemPrompt, "Guidelines") {
		t.Error("ChatSystemPrompt does not contain 'Guidelines'")
	}
}

func TestResearcherSystemPrompt_NotEmpty(t *testing.T) {
	if ResearcherSystemPrompt == "" {
		t.Fatal("ResearcherSystemPrompt is empty")
	}
}

func TestResearcherSystemPrompt_ContainsReadOnlyTools(t *testing.T) {
	tools := []string{"read", "glob", "grep"}
	for _, tool := range tools {
		if !strings.Contains(ResearcherSystemPrompt, tool) {
			t.Errorf("ResearcherSystemPrompt does not mention tool %q", tool)
		}
	}
}

func TestResearcherSystemPrompt_MentionsReadOnly(t *testing.T) {
	if !strings.Contains(ResearcherSystemPrompt, "read-only") {
		t.Error("ResearcherSystemPrompt does not mention 'read-only'")
	}
}

func TestReviewerSystemPrompt_NotEmpty(t *testing.T) {
	if ReviewerSystemPrompt == "" {
		t.Fatal("ReviewerSystemPrompt is empty")
	}
}

func TestReviewerSystemPrompt_ContainsReviewTools(t *testing.T) {
	tools := []string{"read", "glob", "grep", "bash"}
	for _, tool := range tools {
		if !strings.Contains(ReviewerSystemPrompt, tool) {
			t.Errorf("ReviewerSystemPrompt does not mention tool %q", tool)
		}
	}
}

func TestPlannerSystemPrompt_NotEmpty(t *testing.T) {
	if PlannerSystemPrompt == "" {
		t.Fatal("PlannerSystemPrompt is empty")
	}
}

func TestPlannerSystemPrompt_ContainsJSONSchema(t *testing.T) {
	if !strings.Contains(PlannerSystemPrompt, "goal") {
		t.Error("PlannerSystemPrompt does not contain 'goal'")
	}
	if !strings.Contains(PlannerSystemPrompt, "steps") {
		t.Error("PlannerSystemPrompt does not contain 'steps'")
	}
	if !strings.Contains(PlannerSystemPrompt, "depends_on") {
		t.Error("PlannerSystemPrompt does not contain 'depends_on'")
	}
}

func TestPlanSummaryPrompt_NotEmpty(t *testing.T) {
	if PlanSummaryPrompt == "" {
		t.Fatal("PlanSummaryPrompt is empty")
	}
}
