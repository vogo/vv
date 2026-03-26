package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestTruncate_Short(t *testing.T) {
	result := truncate("hello", 10)
	if result != "hello" {
		t.Errorf("truncate = %q, want %q", result, "hello")
	}
}

func TestTruncate_Long(t *testing.T) {
	result := truncate("hello world this is long", 10)
	if !strings.HasSuffix(result, "...") {
		t.Errorf("truncate should end with '...', got %q", result)
	}

	if len(result) != 13 { // 10 + "..."
		t.Errorf("truncate len = %d, want 13", len(result))
	}
}

func TestTruncate_Exact(t *testing.T) {
	result := truncate("hello", 5)
	if result != "hello" {
		t.Errorf("truncate = %q, want %q", result, "hello")
	}
}

func TestRenderError(t *testing.T) {
	result := renderError(errors.New("something failed"))
	if !strings.Contains(result, "something failed") {
		t.Errorf("renderError should contain error message, got %q", result)
	}
}

func TestRenderUserMessage(t *testing.T) {
	result := renderUserMessage("hello")
	if !strings.Contains(result, "hello") {
		t.Errorf("renderUserMessage should contain text, got %q", result)
	}
}

func TestRenderSystemMessage(t *testing.T) {
	result := renderSystemMessage("system info")
	if !strings.Contains(result, "system info") {
		t.Errorf("renderSystemMessage should contain text, got %q", result)
	}
}

func TestRenderToolMessage(t *testing.T) {
	result := renderToolMessage("tool output")
	if !strings.Contains(result, "tool output") {
		t.Errorf("renderToolMessage should contain text, got %q", result)
	}
}

func TestRenderMarkdown(t *testing.T) {
	result := renderMarkdown("**bold**", 80)
	// Glamour should render bold text somehow.
	if result == "" {
		t.Error("renderMarkdown should produce non-empty output")
	}
}

func TestRenderMarkdown_ZeroWidth(t *testing.T) {
	result := renderMarkdown("hello", 0)
	if !strings.Contains(result, "hello") {
		t.Errorf("renderMarkdown with zero width should still contain text, got %q", result)
	}
}

func TestIsExitCommand(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"/exit", true},
		{"/quit", true},
		{" /exit ", true},
		{" /quit ", true},
		{"exit", false},
		{"quit", false},
		{"hello", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isExitCommand(tt.input)
		if got != tt.want {
			t.Errorf("isExitCommand(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
