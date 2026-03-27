package agents

import (
	"strings"
	"testing"
)

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
