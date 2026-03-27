package agents

import (
	"strings"
	"testing"
)

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
