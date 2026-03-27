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
