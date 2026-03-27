package agents

import (
	"strings"
	"testing"
)

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
