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

func TestIndentBlock_ZeroDepth(t *testing.T) {
	result := indentBlock("hello", 0)
	if result != "hello" {
		t.Errorf("indentBlock(\"hello\", 0) = %q, want %q", result, "hello")
	}
}

func TestIndentBlock_SingleLevel(t *testing.T) {
	result := indentBlock("hello", 1)
	want := "    hello"
	if result != want {
		t.Errorf("indentBlock(\"hello\", 1) = %q, want %q", result, want)
	}
}

func TestIndentBlock_MultiLevel(t *testing.T) {
	result := indentBlock("hello", 2)
	want := "        hello"
	if result != want {
		t.Errorf("indentBlock(\"hello\", 2) = %q, want %q", result, want)
	}
}

func TestIndentBlock_Multiline(t *testing.T) {
	input := "line1\nline2\n\nline3"
	result := indentBlock(input, 1)
	want := "    line1\n    line2\n\n    line3"
	if result != want {
		t.Errorf("indentBlock multiline = %q, want %q", result, want)
	}
}

func TestIndentBlock_EmptyString(t *testing.T) {
	result := indentBlock("", 1)
	if result != "" {
		t.Errorf("indentBlock(\"\", 1) = %q, want %q", result, "")
	}
}

func TestRenderToolCallStart_Indented(t *testing.T) {
	result := renderToolCallStart("bash", `{"command":"ls"}`, 1)
	if !strings.HasPrefix(result, "    ") {
		t.Errorf("renderToolCallStart at depth 1 should start with 4 spaces, got %q", result)
	}
	if !strings.Contains(result, "bash") {
		t.Errorf("renderToolCallStart should contain tool name, got %q", result)
	}
}

func TestRenderToolCallStart_Depth2(t *testing.T) {
	result := renderToolCallStart("bash", `{"command":"ls"}`, 2)
	if !strings.HasPrefix(result, "        ") {
		t.Errorf("renderToolCallStart at depth 2 should start with 8 spaces, got %q", result)
	}
}

func TestRenderToolCallResult_Indented(t *testing.T) {
	result := renderToolCallResult("bash", "output text", 1)
	if !strings.HasPrefix(result, "    ") {
		t.Errorf("renderToolCallResult at depth 1 should start with 4 spaces, got %q", result)
	}
}

func TestRenderSubAgentStart_Indented(t *testing.T) {
	result := renderSubAgentStart("coder", "step1", "do coding", 1, 3, 1)
	if !strings.HasPrefix(result, "    ") {
		t.Errorf("renderSubAgentStart at depth 1 should start with 4 spaces, got %q", result)
	}
	if !strings.Contains(result, "coder") {
		t.Errorf("renderSubAgentStart should contain agent name, got %q", result)
	}
}

func TestRenderSubAgentEnd_Indented(t *testing.T) {
	result := renderSubAgentEnd("coder", "step1", 5000, 3, 1500, 1)
	if !strings.HasPrefix(result, "    ") {
		t.Errorf("renderSubAgentEnd at depth 1 should start with 4 spaces, got %q", result)
	}
	if !strings.Contains(result, "coder") {
		t.Errorf("renderSubAgentEnd should contain agent name, got %q", result)
	}
}

func TestRenderPhaseTransition_NoIndent(t *testing.T) {
	result := renderPhaseTransition("explore", 1, 3, true)
	if strings.HasPrefix(result, " ") {
		t.Errorf("renderPhaseTransition should not start with spaces, got %q", result)
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
