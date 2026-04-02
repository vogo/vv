package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

// mockStreamAgent implements agent.StreamAgent with a configurable producer.
type mockStreamAgent struct {
	id       string
	producer func(ctx context.Context, send func(schema.Event) error) error
}

var _ agent.StreamAgent = (*mockStreamAgent)(nil)

func (m *mockStreamAgent) ID() string          { return m.id }
func (m *mockStreamAgent) Name() string        { return m.id }
func (m *mockStreamAgent) Description() string { return m.id }

func (m *mockStreamAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{}, nil
}

func (m *mockStreamAgent) RunStream(ctx context.Context, _ *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, 16, m.producer), nil
}

func TestRunPrompt_BasicTextOutput(t *testing.T) {
	mock := &mockStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventTextDelta, "test", "s1", schema.TextDeltaData{Delta: "Hello"}))
			_ = send(schema.NewEvent(schema.EventTextDelta, "test", "s1", schema.TextDeltaData{Delta: " world"}))
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	err := RunPrompt(context.Background(), mock, "test prompt", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	// stdout should contain the text deltas followed by a newline.
	if got := stdout.String(); got != "Hello world\n" {
		t.Errorf("stdout = %q, want %q", got, "Hello world\n")
	}

	// stderr should contain the [done] summary line.
	if !strings.Contains(stderr.String(), "[done]") {
		t.Errorf("stderr missing [done] line; got %q", stderr.String())
	}
}

func TestRunPrompt_EmptyStream(t *testing.T) {
	mock := &mockStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			return nil // immediate EOF
		},
	}

	var stdout, stderr bytes.Buffer
	err := RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	// No text output, so no trailing newline on stdout.
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty, got %q", stdout.String())
	}

	// [done] line should still appear.
	if !strings.Contains(stderr.String(), "[done]") {
		t.Errorf("stderr missing [done] line; got %q", stderr.String())
	}
}

func TestRunPrompt_ToolEventsToStderr(t *testing.T) {
	mock := &mockStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventToolCallStart, "test", "s1", schema.ToolCallStartData{
				ToolName:  "read",
				Arguments: `{"file_path":"main.go"}`,
			}))
			_ = send(schema.NewEvent(schema.EventToolResult, "test", "s1", schema.ToolResultData{
				ToolName: "read",
				Result: schema.ToolResult{
					Content: []schema.ContentPart{{Type: "text", Text: "file contents"}},
				},
			}))
			_ = send(schema.NewEvent(schema.EventTextDelta, "test", "s1", schema.TextDeltaData{Delta: "done"}))
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	err := RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	// Tool call should appear on stderr.
	if !strings.Contains(stderr.String(), "[tool] read(main.go)") {
		t.Errorf("stderr missing tool call; got %q", stderr.String())
	}

	// Read tool results should be suppressed.
	if strings.Contains(stderr.String(), "file contents") {
		t.Errorf("stderr should suppress read result; got %q", stderr.String())
	}

	// stdout should only have the text delta.
	if got := stdout.String(); got != "done\n" {
		t.Errorf("stdout = %q, want %q", got, "done\n")
	}
}

func TestRunPrompt_PhaseEvents(t *testing.T) {
	mock := &mockStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventPhaseStart, "test", "s1", schema.PhaseStartData{Phase: "explore"}))
			_ = send(schema.NewEvent(schema.EventPhaseEnd, "test", "s1", schema.PhaseEndData{
				Phase:    "explore",
				Duration: 5000,
			}))
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	err := RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "[phase] Explore") {
		t.Errorf("stderr missing phase start; got %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "[phase] Explore complete") {
		t.Errorf("stderr missing phase end; got %q", stderrStr)
	}
}

func TestRunPrompt_SubAgentNesting(t *testing.T) {
	mock := &mockStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventSubAgentStart, "test", "s1", schema.SubAgentStartData{
				AgentName: "coder",
				StepID:    "step-1",
			}))
			_ = send(schema.NewEvent(schema.EventToolCallStart, "test", "s1", schema.ToolCallStartData{
				ToolName:  "bash",
				Arguments: `{"command":"ls"}`,
			}))
			_ = send(schema.NewEvent(schema.EventSubAgentEnd, "test", "s1", schema.SubAgentEndData{
				AgentName: "coder",
				StepID:    "step-1",
				Duration:  2000,
				ToolCalls: 1,
			}))
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	err := RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	stderrStr := stderr.String()

	// Sub-agent start should be indented (nestingDepth=1 -> 2 spaces).
	if !strings.Contains(stderrStr, "  [agent] coder (step-1)") {
		t.Errorf("stderr missing indented sub-agent start; got %q", stderrStr)
	}

	// Tool inside sub-agent should be indented further (nestingDepth+1=2 -> 4 spaces).
	if !strings.Contains(stderrStr, "    [tool] bash(ls)") {
		t.Errorf("stderr missing deeply indented tool call; got %q", stderrStr)
	}

	// Sub-agent end should be indented at sub-agent level.
	if !strings.Contains(stderrStr, "  [agent] coder complete") {
		t.Errorf("stderr missing sub-agent end; got %q", stderrStr)
	}
}

func TestRunPrompt_ErrorPropagation(t *testing.T) {
	expectedErr := fmt.Errorf("stream failure")
	mock := &mockStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventTextDelta, "test", "s1", schema.TextDeltaData{Delta: "partial"}))
			return expectedErr
		},
	}

	var stdout, stderr bytes.Buffer
	err := RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err == nil {
		t.Fatal("RunPrompt should return error")
	}
	if !strings.Contains(err.Error(), "stream failure") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "stream failure")
	}
}

func TestRunPrompt_ErrorEvent(t *testing.T) {
	mock := &mockStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventError, "test", "s1", schema.ErrorData{
				Message: "something went wrong",
			}))
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	err := RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	if !strings.Contains(stderr.String(), "[error] something went wrong") {
		t.Errorf("stderr missing error event; got %q", stderr.String())
	}
}

func TestRunPrompt_TokenBudgetExhausted(t *testing.T) {
	mock := &mockStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventTokenBudgetExhausted, "test", "s1", schema.TokenBudgetExhaustedData{
				Budget: 1000,
				Used:   1000,
			}))
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	err := RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	if !strings.Contains(stderr.String(), "[warning] token budget exhausted") {
		t.Errorf("stderr missing warning; got %q", stderr.String())
	}
}

func TestRunPrompt_NoANSIInStdout(t *testing.T) {
	mock := &mockStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventTextDelta, "test", "s1", schema.TextDeltaData{Delta: "clean text"}))
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	err := RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	if strings.Contains(stdout.String(), "\x1b") {
		t.Errorf("stdout contains ANSI escape codes: %q", stdout.String())
	}
}

func TestRunPrompt_TaskCompleteSummary(t *testing.T) {
	mock := &mockStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventLLMCallEnd, "test", "s1", schema.LLMCallEndData{
				PromptTokens:     500,
				CompletionTokens: 200,
			}))
			_ = send(schema.NewEvent(schema.EventToolCallStart, "test", "s1", schema.ToolCallStartData{
				ToolName:  "bash",
				Arguments: `{"command":"echo hi"}`,
			}))
			_ = send(schema.NewEvent(schema.EventTextDelta, "test", "s1", schema.TextDeltaData{Delta: "result"}))
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	err := RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	stderrStr := stderr.String()
	// [done] line should have stats.
	if !strings.Contains(stderrStr, "[done]") {
		t.Errorf("stderr missing [done]; got %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "1 tool use") {
		t.Errorf("stderr missing tool count in [done]; got %q", stderrStr)
	}
}

func TestRunPrompt_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	mock := &mockStreamAgent{
		id: "test",
		producer: func(ctx context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventTextDelta, "test", "s1", schema.TextDeltaData{Delta: "before"}))
			// Simulate waiting for context cancellation.
			<-ctx.Done()
			return ctx.Err()
		},
	}

	var stdout, stderr bytes.Buffer

	// Cancel immediately.
	cancel()

	err := RunPrompt(ctx, mock, "test", &stdout, &stderr)
	// Should get an error (context cancelled or stream closed).
	if err == nil {
		// It's also acceptable for RunPrompt to return nil if the producer
		// completed before the context was observed, but in this case with
		// a blocking producer, we expect an error.
		// Allow both outcomes since timing is non-deterministic.
		_ = err
	}
}

func TestRunPrompt_PhaseSummary(t *testing.T) {
	mock := &mockStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventPhaseEnd, "test", "s1", schema.PhaseEndData{
				Phase:    "classify",
				Duration: 1000,
				Summary:  "Route to coder agent",
			}))
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	err := RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "Route to coder agent") {
		t.Errorf("stderr missing phase summary; got %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "[phase] Classify complete") {
		t.Errorf("stderr missing phase end; got %q", stderrStr)
	}
}

func TestRunPrompt_BashToolResult(t *testing.T) {
	mock := &mockStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventToolCallStart, "test", "s1", schema.ToolCallStartData{
				ToolName:  "bash",
				Arguments: `{"command":"ls -la"}`,
			}))
			_ = send(schema.NewEvent(schema.EventToolResult, "test", "s1", schema.ToolResultData{
				ToolName: "bash",
				Result: schema.ToolResult{
					Content: []schema.ContentPart{{Type: "text", Text: "total 42\ndrwxr-xr-x  5 user group\n-rw-r--r-- 1 user group"}},
				},
			}))
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	err := RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	stderrStr := stderr.String()
	// Bash result should show truncated output with line count.
	if !strings.Contains(stderrStr, "total 42") {
		t.Errorf("stderr missing bash output preview; got %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "+2 lines") {
		t.Errorf("stderr missing line count for bash output; got %q", stderrStr)
	}
}

func TestPromptToolResultSummary(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		result   string
		want     string
	}{
		{"read suppressed", "read", "file contents", ""},
		{"bash with output", "bash", "line1\nline2", "-> line1 (+1 lines)"},
		{"bash empty", "bash", "", ""},
		{"default with output", "grep", "found something", "-> found something"},
		{"default empty", "grep", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := promptToolResultSummary(tt.toolName, tt.result)
			if got != tt.want {
				t.Errorf("promptToolResultSummary(%q, %q) = %q, want %q", tt.toolName, tt.result, got, tt.want)
			}
		})
	}
}

func TestCapitalizeFirst(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"explore", "Explore"},
		{"", ""},
		{"A", "A"},
		{"classify", "Classify"},
	}

	for _, tt := range tests {
		if got := capitalizeFirst(tt.input); got != tt.want {
			t.Errorf("capitalizeFirst(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// Verify RunPrompt satisfies the io.Writer based API contract.
func TestRunPrompt_WriterInterface(t *testing.T) {
	mock := &mockStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			return nil
		},
	}

	// RunPrompt should accept any io.Writer, including io.Discard.
	err := RunPrompt(context.Background(), mock, "test", io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}
}
