package cli

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/vogo/vage/schema"
)

// stripANSI removes ANSI color escape sequences so assertions can match the
// plain text content of styled output.
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiEscapeRe.ReplaceAllString(s, "") }

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

func TestRenderSummaryMessage(t *testing.T) {
	result := renderSummaryMessage("This is a summary of previous context.")
	if !strings.Contains(result, "Previous context (summarized)") {
		t.Errorf("renderSummaryMessage should contain header, got %q", result)
	}
	if !strings.Contains(result, "summary of previous context") {
		t.Errorf("renderSummaryMessage should contain body text, got %q", result)
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
	stats := execStats{
		ToolCalls:        3,
		DurationMs:       5000,
		PromptTokens:     1000,
		CompletionTokens: 500,
	}
	result := renderSubAgentEnd("coder", "step1", stats, 1)
	if !strings.HasPrefix(result, "    ") {
		t.Errorf("renderSubAgentEnd at depth 1 should start with 4 spaces, got %q", result)
	}
	if !strings.Contains(result, "coder") {
		t.Errorf("renderSubAgentEnd should contain agent name, got %q", result)
	}
	if !strings.Contains(result, "complete.") {
		t.Errorf("renderSubAgentEnd should contain 'complete.', got %q", result)
	}
}

func TestRenderPhaseTransition_StartNoIndent(t *testing.T) {
	result := renderPhaseTransition("explore", true, execStats{}, 0)
	if strings.HasPrefix(result, " ") {
		t.Errorf("renderPhaseTransition start should not start with spaces, got %q", result)
	}
}

func TestRenderPhaseTransition_EndIndented(t *testing.T) {
	result := renderPhaseTransition("explore", false, execStats{DurationMs: 1000}, 1)
	if !strings.HasPrefix(result, "    ") {
		t.Errorf("renderPhaseTransition end should be indented 4 spaces, got %q", result)
	}
	if !strings.Contains(result, "complete.") {
		t.Errorf("renderPhaseTransition end should contain 'complete.', got %q", result)
	}
}

func TestFormatCompactTokens(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0k"},
		{5300, "5.3k"},
		{1200000, "1.2M"},
	}

	for _, tt := range tests {
		got := formatCompactTokens(tt.input)
		if got != tt.want {
			t.Errorf("formatCompactTokens(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildStatsLine(t *testing.T) {
	s := execStats{
		ToolCalls:        3,
		DurationMs:       26000,
		PromptTokens:     5300,
		CompletionTokens: 10500,
	}
	result := buildStatsLine(s)
	if !strings.Contains(result, "3 tool uses") {
		t.Errorf("buildStatsLine should contain '3 tool uses', got %q", result)
	}
	if !strings.Contains(result, "26s") {
		t.Errorf("buildStatsLine should contain '26s', got %q", result)
	}
	if !strings.Contains(result, "\u2191 5.3k") {
		t.Errorf("buildStatsLine should contain up arrow with 5.3k, got %q", result)
	}
	if !strings.Contains(result, "\u2193 10.5k") {
		t.Errorf("buildStatsLine should contain down arrow with 10.5k, got %q", result)
	}
	if !strings.Contains(result, "\u00b7") {
		t.Errorf("buildStatsLine should contain middle dot separator, got %q", result)
	}
}

func TestBuildStatsLine_DurationOnly(t *testing.T) {
	s := execStats{DurationMs: 500}
	result := buildStatsLine(s)
	if result != "(500ms)" {
		t.Errorf("buildStatsLine duration only = %q, want %q", result, "(500ms)")
	}
}

func TestBuildStatsLine_SingularToolUse(t *testing.T) {
	s := execStats{ToolCalls: 1, DurationMs: 2000}
	result := buildStatsLine(s)
	if !strings.Contains(result, "1 tool use") {
		t.Errorf("buildStatsLine should contain '1 tool use' (singular), got %q", result)
	}
	if strings.Contains(result, "tool uses") {
		t.Errorf("buildStatsLine should not contain 'tool uses' (plural) for 1, got %q", result)
	}
}

func TestBuildStatsLine_ZeroToolCallsOmitted(t *testing.T) {
	s := execStats{DurationMs: 1000, PromptTokens: 100}
	result := buildStatsLine(s)
	if strings.Contains(result, "tool") {
		t.Errorf("buildStatsLine should not contain 'tool' when ToolCalls=0, got %q", result)
	}
}

func TestBuildStatsLine_ZeroTokensOmitted(t *testing.T) {
	s := execStats{DurationMs: 1000}
	result := buildStatsLine(s)
	if strings.Contains(result, "\u2191") || strings.Contains(result, "\u2193") {
		t.Errorf("buildStatsLine should not contain arrows when tokens=0, got %q", result)
	}
}

func TestRenderPhaseTransition_End(t *testing.T) {
	stats := execStats{
		ToolCalls:        2,
		DurationMs:       3000,
		PromptTokens:     1000,
		CompletionTokens: 500,
	}
	result := renderPhaseTransition("dispatch", false, stats, 1)
	if !strings.Contains(result, "phase Dispatch complete.") {
		t.Errorf("renderPhaseTransition end should contain 'phase Dispatch complete.', got %q", result)
	}
	if !strings.Contains(result, "2 tool uses") {
		t.Errorf("renderPhaseTransition end should contain tool use stats, got %q", result)
	}
}

func TestRenderPhaseTransition_Start(t *testing.T) {
	stats := execStats{ToolCalls: 5, DurationMs: 999, PromptTokens: 100}
	result := renderPhaseTransition("explore", true, stats, 0)
	// Starting mode should show phase name but ignore stats.
	if !strings.Contains(result, "Explore") {
		t.Errorf("renderPhaseTransition start should contain 'Explore', got %q", result)
	}
	if strings.Contains(result, "tool") {
		t.Errorf("renderPhaseTransition start should not contain stats, got %q", result)
	}
}

func TestRenderSubAgentEnd_WithTokenBreakdown(t *testing.T) {
	stats := execStats{
		ToolCalls:        5,
		DurationMs:       10000,
		PromptTokens:     2000,
		CompletionTokens: 800,
	}
	result := renderSubAgentEnd("researcher", "", stats, 0)
	if !strings.Contains(result, "sub-agent") {
		t.Errorf("renderSubAgentEnd should contain 'sub-agent', got %q", result)
	}
	if !strings.Contains(result, "researcher") {
		t.Errorf("renderSubAgentEnd should contain agent name, got %q", result)
	}
	if !strings.Contains(result, "complete.") {
		t.Errorf("renderSubAgentEnd should contain 'complete.', got %q", result)
	}
	if !strings.Contains(result, "\u2191 2.0k") {
		t.Errorf("renderSubAgentEnd should contain prompt tokens, got %q", result)
	}
	if !strings.Contains(result, "\u2193 800") {
		t.Errorf("renderSubAgentEnd should contain completion tokens, got %q", result)
	}
}

func TestRenderSubAgentEnd_DurationOnly(t *testing.T) {
	stats := execStats{DurationMs: 3000}
	result := renderSubAgentEnd("chat", "", stats, 0)
	if !strings.Contains(result, "3s") {
		t.Errorf("renderSubAgentEnd should contain duration, got %q", result)
	}
	if strings.Contains(result, "\u2191") || strings.Contains(result, "\u2193") {
		t.Errorf("renderSubAgentEnd should not contain token arrows with zero tokens, got %q", result)
	}
}

func TestRenderTaskComplete(t *testing.T) {
	stats := execStats{
		DurationMs:       5000,
		PromptTokens:     10000,
		CompletionTokens: 3000,
	}
	result := renderTaskComplete(stats)
	if !strings.Contains(result, "task complete.") {
		t.Errorf("renderTaskComplete should contain 'task complete.', got %q", result)
	}
	if !strings.Contains(result, "5s") {
		t.Errorf("renderTaskComplete should contain duration, got %q", result)
	}
	if !strings.Contains(result, "\u2191 10.0k") {
		t.Errorf("renderTaskComplete should contain prompt tokens, got %q", result)
	}
}

func TestRenderTaskComplete_NoTokens(t *testing.T) {
	stats := execStats{DurationMs: 2000}
	result := renderTaskComplete(stats)
	if !strings.Contains(result, "task complete.") {
		t.Errorf("renderTaskComplete should contain 'task complete.', got %q", result)
	}
	if !strings.Contains(result, "2s") {
		t.Errorf("renderTaskComplete should contain duration, got %q", result)
	}
	if strings.Contains(result, "\u2191") || strings.Contains(result, "\u2193") {
		t.Errorf("renderTaskComplete should not contain token arrows with zero tokens, got %q", result)
	}
}

func TestShortModelName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"claude-sonnet-4-20250514", "sonnet-4"},
		{"claude-opus-4-20250514", "opus-4"},
		{"claude-sonnet-4", "sonnet-4"},
		{"gpt-4o-20240806", "gpt-4o"},
		{"gpt-4o", "gpt-4o"},
		{"gpt-4o-mini", "gpt-4o-mini"},
		{"my-custom-model", "my-custom-model"},
	}

	for _, tt := range tests {
		got := shortModelName(tt.input)
		if got != tt.want {
			t.Errorf("shortModelName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatCost(t *testing.T) {
	tests := []struct {
		name string
		cost *float64
		want string
	}{
		{"nil", nil, "N/A"},
		{"zero", new(0.0), "$0.000"},
		{"small", new(0.042), "$0.042"},
		{"large", new(1.5), "$1.50"},
		{"exact dollar", new(1.0), "$1.00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCost(tt.cost)
			if got != tt.want {
				t.Errorf("formatCost(%v) = %q, want %q", tt.cost, got, tt.want)
			}
		})
	}
}

func TestBuildStatsLine_WithCacheReadTokens(t *testing.T) {
	s := execStats{
		DurationMs:       5000,
		PromptTokens:     1000,
		CompletionTokens: 500,
		CacheReadTokens:  200,
	}
	result := buildStatsLine(s)
	if !strings.Contains(result, "cache 200") {
		t.Errorf("buildStatsLine should contain 'cache 200', got %q", result)
	}
}

func TestBuildStatsLine_ZeroCacheReadTokensOmitted(t *testing.T) {
	s := execStats{
		DurationMs:       5000,
		PromptTokens:     1000,
		CompletionTokens: 500,
	}
	result := buildStatsLine(s)
	if strings.Contains(result, "cache") {
		t.Errorf("buildStatsLine should not contain 'cache' when CacheReadTokens=0, got %q", result)
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

func TestRenderTodoList_AllStatuses(t *testing.T) {
	data := schema.TodoUpdateData{
		Version: 3,
		Items: []schema.TodoItem{
			{ID: "todo_1", Content: "Read existing code", ActiveForm: "Reading existing code", Status: "completed"},
			{ID: "todo_2", Content: "Refactor foo module", ActiveForm: "Refactoring foo module", Status: "in_progress"},
			{ID: "todo_3", Content: "Update unit tests", ActiveForm: "Updating unit tests", Status: "pending"},
		},
	}
	rendered := renderTodoList(data, 0)
	plain := stripANSI(rendered)

	if !strings.Contains(plain, "Todos (v3, 3 items)") {
		t.Errorf("missing header, got: %q", plain)
	}
	if !strings.Contains(plain, "[x] Read existing code") {
		t.Errorf("missing completed marker, got: %q", plain)
	}
	// in_progress must show ActiveForm, not Content.
	if !strings.Contains(plain, "[>] Refactoring foo module") {
		t.Errorf("missing in_progress marker/active form, got: %q", plain)
	}
	if strings.Contains(plain, "Refactor foo module") {
		t.Errorf("in_progress row must use active_form, not content, got: %q", plain)
	}
	if !strings.Contains(plain, "[ ] Update unit tests") {
		t.Errorf("missing pending marker, got: %q", plain)
	}
}

func TestRenderTodoList_Empty(t *testing.T) {
	data := schema.TodoUpdateData{Version: 1}
	rendered := renderTodoList(data, 0)
	plain := stripANSI(rendered)
	if !strings.Contains(plain, "Todos (v1, 0 items)") {
		t.Errorf("missing header for empty list, got: %q", plain)
	}
	// No checkbox markers should appear.
	for _, marker := range []string{"[x]", "[>]", "[ ]"} {
		if strings.Contains(plain, marker) {
			t.Errorf("empty list should not render item marker %q; got: %q", marker, plain)
		}
	}
}

func TestRenderTodoList_IndentDepth(t *testing.T) {
	data := schema.TodoUpdateData{Version: 1, Items: []schema.TodoItem{
		{Content: "A", ActiveForm: "Doing A", Status: "pending"},
	}}
	// Depth 2 should prepend 2*indentUnit spaces to the first line.
	rendered := renderTodoList(data, 2)
	plain := stripANSI(rendered)
	wantPrefix := strings.Repeat(" ", 2*indentUnit) + "Todos"
	if !strings.HasPrefix(plain, wantPrefix) {
		t.Errorf("expected depth-2 indent %q, got: %q", wantPrefix, plain)
	}
}

// TestRenderToolCallResult_SuppressesTodoWrite is a regression guard for the
// double-print suppression: renderTodoList is the canonical surface for
// EventTodoUpdate, so the tool_result for "todo_write" must render as an
// empty string and avoid a second, text-only row in the scrollback.
func TestRenderToolCallResult_SuppressesTodoWrite(t *testing.T) {
	got := renderToolCallResult("todo_write", "ok (v2, 3 items)", 1)
	if got != "" {
		t.Errorf("renderToolCallResult(todo_write) must return empty string, got %q", got)
	}
}
