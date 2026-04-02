package prompt_tests

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	vvcli "github.com/vogo/vv/cli"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
)

// stubStreamAgent implements agent.StreamAgent for testing.
type stubStreamAgent struct {
	id       string
	response string
}

var _ agent.StreamAgent = (*stubStreamAgent)(nil)

func (s *stubStreamAgent) ID() string          { return s.id }
func (s *stubStreamAgent) Name() string        { return s.id }
func (s *stubStreamAgent) Description() string { return s.id }

func (s *stubStreamAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(s.response),
			}, s.id),
		},
	}, nil
}

func (s *stubStreamAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, 8, func(_ context.Context, send func(schema.Event) error) error {
		if err := send(schema.NewEvent(schema.EventAgentStart, s.id, req.SessionID, schema.AgentStartData{})); err != nil {
			return err
		}

		if err := send(schema.NewEvent(schema.EventTextDelta, s.id, req.SessionID, schema.TextDeltaData{Delta: s.response})); err != nil {
			return err
		}

		return send(schema.NewEvent(schema.EventAgentEnd, s.id, req.SessionID, schema.AgentEndData{
			Message: s.response,
		}))
	}), nil
}

// --- Test: RunPrompt with stubStreamAgent produces correct stdout/stderr ---
// Verifies that cli.RunPrompt writes text deltas to stdout only and diagnostic
// events (phases, tools, agents, done) to stderr only.
// Covers: acceptance criteria for stdout/stderr separation and pipe-friendly output.
func TestIntegration_Prompt_RunPromptStdoutStderr(t *testing.T) {
	mock := &stubStreamAgent{id: "test-orchestrator", response: "hello world"}

	var stdout, stderr bytes.Buffer

	err := vvcli.RunPrompt(context.Background(), mock, "test prompt", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	// stdout should contain the text delta followed by a newline.
	if got := stdout.String(); got != "hello world\n" {
		t.Errorf("stdout = %q, want %q", got, "hello world\n")
	}

	// stdout should contain no ANSI escape codes (pipe-friendly).
	if strings.Contains(stdout.String(), "\x1b") {
		t.Errorf("stdout contains ANSI escape codes: %q", stdout.String())
	}

	// stderr should contain the [done] summary line.
	if !strings.Contains(stderr.String(), "[done]") {
		t.Errorf("stderr missing [done] line; got %q", stderr.String())
	}

	// stdout should not contain diagnostic tags.
	for _, tag := range []string{"[done]", "[phase]", "[tool]", "[agent]", "[error]"} {
		if strings.Contains(stdout.String(), tag) {
			t.Errorf("stdout should not contain %q; got %q", tag, stdout.String())
		}
	}
}

// --- Test: RunPrompt with full event sequence (phases, tools, sub-agents) ---
// Verifies that a realistic event stream is correctly rendered: phases on stderr,
// tool calls indented under sub-agents, and [done] summary with stats.
// Covers: diagnostic stderr output format, nesting/indentation, stats accumulation.
func TestIntegration_Prompt_RunPromptFullEventStream(t *testing.T) {
	mock := &mockPromptStreamAgent{
		id: "orchestrator",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			// Phase: Explore
			_ = send(schema.NewEvent(schema.EventPhaseStart, "orchestrator", "s1", schema.PhaseStartData{Phase: "explore"}))
			_ = send(schema.NewEvent(schema.EventPhaseEnd, "orchestrator", "s1", schema.PhaseEndData{
				Phase:    "explore",
				Duration: 5000,
			}))

			// Phase: Classify
			_ = send(schema.NewEvent(schema.EventPhaseStart, "orchestrator", "s1", schema.PhaseStartData{Phase: "classify"}))
			_ = send(schema.NewEvent(schema.EventPhaseEnd, "orchestrator", "s1", schema.PhaseEndData{
				Phase:    "classify",
				Duration: 1000,
				Summary:  "Route to coder agent",
			}))

			// Phase: Dispatch with sub-agent
			_ = send(schema.NewEvent(schema.EventPhaseStart, "orchestrator", "s1", schema.PhaseStartData{Phase: "dispatch"}))

			_ = send(schema.NewEvent(schema.EventSubAgentStart, "orchestrator", "s1", schema.SubAgentStartData{
				AgentName: "coder",
				StepID:    "step-1",
			}))
			_ = send(schema.NewEvent(schema.EventLLMCallEnd, "orchestrator", "s1", schema.LLMCallEndData{
				PromptTokens:     500,
				CompletionTokens: 200,
			}))
			_ = send(schema.NewEvent(schema.EventToolCallStart, "orchestrator", "s1", schema.ToolCallStartData{
				ToolName:  "read",
				Arguments: `{"file_path":"main.go"}`,
			}))
			_ = send(schema.NewEvent(schema.EventToolResult, "orchestrator", "s1", schema.ToolResultData{
				ToolName: "read",
				Result:   schema.ToolResult{Content: []schema.ContentPart{{Type: "text", Text: "file contents"}}},
			}))
			_ = send(schema.NewEvent(schema.EventToolCallStart, "orchestrator", "s1", schema.ToolCallStartData{
				ToolName:  "edit",
				Arguments: `{"file_path":"main.go"}`,
			}))
			_ = send(schema.NewEvent(schema.EventToolResult, "orchestrator", "s1", schema.ToolResultData{
				ToolName: "edit",
				Result:   schema.ToolResult{Content: []schema.ContentPart{{Type: "text", Text: "+new line\n-old line"}}},
			}))

			// Text output
			_ = send(schema.NewEvent(schema.EventTextDelta, "orchestrator", "s1", schema.TextDeltaData{Delta: "Changes applied"}))

			_ = send(schema.NewEvent(schema.EventSubAgentEnd, "orchestrator", "s1", schema.SubAgentEndData{
				AgentName: "coder",
				StepID:    "step-1",
				Duration:  8000,
				ToolCalls: 2,
			}))

			_ = send(schema.NewEvent(schema.EventPhaseEnd, "orchestrator", "s1", schema.PhaseEndData{
				Phase:    "dispatch",
				Duration: 8000,
			}))

			return nil
		},
	}

	var stdout, stderr bytes.Buffer

	err := vvcli.RunPrompt(context.Background(), mock, "fix the bug", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	// Verify stdout contains only text output.
	if got := stdout.String(); got != "Changes applied\n" {
		t.Errorf("stdout = %q, want %q", got, "Changes applied\n")
	}

	stderrStr := stderr.String()

	// Verify phase events.
	if !strings.Contains(stderrStr, "[phase] Explore") {
		t.Errorf("stderr missing [phase] Explore; got %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "[phase] Explore complete") {
		t.Errorf("stderr missing Explore complete; got %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "[phase] Classify") {
		t.Errorf("stderr missing [phase] Classify; got %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "Route to coder agent") {
		t.Errorf("stderr missing Classify summary; got %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "[phase] Dispatch") {
		t.Errorf("stderr missing [phase] Dispatch; got %q", stderrStr)
	}

	// Verify sub-agent events with indentation.
	if !strings.Contains(stderrStr, "  [agent] coder (step-1)") {
		t.Errorf("stderr missing indented sub-agent start; got %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "  [agent] coder complete") {
		t.Errorf("stderr missing sub-agent complete; got %q", stderrStr)
	}

	// Verify tool calls indented under sub-agent.
	if !strings.Contains(stderrStr, "    [tool] read(main.go)") {
		t.Errorf("stderr missing indented tool read; got %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "    [tool] edit(main.go)") {
		t.Errorf("stderr missing indented tool edit; got %q", stderrStr)
	}

	// Verify read result is suppressed but edit result is shown.
	if strings.Contains(stderrStr, "file contents") {
		t.Errorf("stderr should suppress read tool result; got %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "Added 1 line") {
		t.Errorf("stderr missing edit result summary; got %q", stderrStr)
	}

	// Verify [done] summary with stats.
	if !strings.Contains(stderrStr, "[done]") {
		t.Errorf("stderr missing [done]; got %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "2 tool uses") {
		t.Errorf("stderr missing tool count in [done]; got %q", stderrStr)
	}
}

// --- Test: RunPrompt with empty stream ---
// Verifies that an empty stream (immediate EOF) results in no stdout output
// and still emits a [done] line on stderr.
// Covers: edge case of empty/no response from agent.
func TestIntegration_Prompt_RunPromptEmptyStream(t *testing.T) {
	mock := &mockPromptStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			return nil // immediate EOF
		},
	}

	var stdout, stderr bytes.Buffer

	err := vvcli.RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	// No text output, so no trailing newline.
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty, got %q", stdout.String())
	}

	// [done] should still appear.
	if !strings.Contains(stderr.String(), "[done]") {
		t.Errorf("stderr missing [done]; got %q", stderr.String())
	}
}

// --- Test: RunPrompt error propagation ---
// Verifies that a stream error is propagated back as the return value.
// Covers: error handling and exit code 1 behavior.
func TestIntegration_Prompt_RunPromptErrorPropagation(t *testing.T) {
	mock := &mockPromptStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventTextDelta, "test", "s1", schema.TextDeltaData{Delta: "partial"}))
			return context.DeadlineExceeded
		},
	}

	var stdout, stderr bytes.Buffer

	err := vvcli.RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err == nil {
		t.Fatal("RunPrompt should return error for stream failure")
	}

	if !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("error = %q, want it to contain 'deadline exceeded'", err.Error())
	}
}

// --- Test: RunPrompt context cancellation ---
// Verifies graceful handling when the context is cancelled during stream consumption.
// Covers: SIGINT/SIGTERM handling behavior (context cancellation is the mechanism).
func TestIntegration_Prompt_RunPromptContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	mock := &mockPromptStreamAgent{
		id: "test",
		producer: func(ctx context.Context, send func(schema.Event) error) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}

	var stdout, stderr bytes.Buffer

	// Should not hang.
	done := make(chan error, 1)
	go func() {
		done <- vvcli.RunPrompt(ctx, mock, "test", &stdout, &stderr)
	}()

	select {
	case <-done:
		// Completed without hanging -- that's the key assertion.
	case <-time.After(5 * time.Second):
		t.Fatal("RunPrompt did not complete within 5 seconds after context cancellation")
	}
}

// --- Test: Empty prompt (`-p ""`) falls through to normal mode ---
// Go's flag package sets -p "" to empty string, which means *promptFlag == ""
// and the prompt-mode branch is never entered. This is equivalent to running
// `vv` without the -p flag. We verify the binary does not crash and exits
// (it will fail on normal CLI startup with a fake key, which is expected).
// Covers: design Test 2 -- empty prompt behavior (flag value is empty string).
func TestIntegration_Prompt_EmptyPromptFallthrough(t *testing.T) {
	binary := buildVVBinary(t)
	defer func() { _ = os.Remove(binary) }()

	cmd := exec.Command(binary, "-p", "")
	cmd.Env = append(os.Environ(), "VV_LLM_API_KEY=fake-key-for-test")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	// Should exit non-zero (falls through to CLI mode, which fails with fake key).
	if err == nil {
		t.Fatal("expected non-zero exit code when -p is empty string")
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 1 {
			t.Errorf("exit code = %d, want 1", exitErr.ExitCode())
		}
	}
}

// --- Test: Whitespace-only prompt rejection via binary ---
// Verifies that `vv -p "   "` (whitespace only) exits with code 1.
// Covers: edge case for prompt validation (trimming).
func TestIntegration_Prompt_WhitespaceOnlyPromptRejection(t *testing.T) {
	binary := buildVVBinary(t)
	defer func() { _ = os.Remove(binary) }()

	cmd := exec.Command(binary, "-p", "   ")
	cmd.Env = append(os.Environ(), "VV_LLM_API_KEY=fake-key-for-test")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit code for whitespace-only prompt")
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 1 {
			t.Errorf("exit code = %d, want 1", exitErr.ExitCode())
		}
	}

	if !strings.Contains(stderr.String(), "non-empty prompt") {
		t.Errorf("stderr = %q, expected to mention 'non-empty prompt'", stderr.String())
	}
}

// --- Test: HTTP mode incompatibility via -mode flag ---
// Verifies that `vv -p "hello" -mode http` exits with code 1.
// Covers: design Test 4 -- HTTP mode incompatibility via flag.
func TestIntegration_Prompt_HTTPModeIncompatibleFlag(t *testing.T) {
	binary := buildVVBinary(t)
	defer func() { _ = os.Remove(binary) }()

	cmd := exec.Command(binary, "-p", "hello", "-mode", "http")
	cmd.Env = append(os.Environ(), "VV_LLM_API_KEY=fake-key-for-test")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit code for -p with -mode http")
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 1 {
			t.Errorf("exit code = %d, want 1", exitErr.ExitCode())
		}
	}

	if !strings.Contains(stderr.String(), "incompatible") {
		t.Errorf("stderr = %q, expected to mention 'incompatible'", stderr.String())
	}
}

// --- Test: HTTP mode incompatibility via VV_MODE env var ---
// Verifies that `VV_MODE=http vv -p "hello"` exits with code 1.
// Covers: design Test 5 -- HTTP mode incompatibility via env.
func TestIntegration_Prompt_HTTPModeIncompatibleEnv(t *testing.T) {
	binary := buildVVBinary(t)
	defer func() { _ = os.Remove(binary) }()

	cmd := exec.Command(binary, "-p", "hello")
	cmd.Env = append(os.Environ(), "VV_LLM_API_KEY=fake-key-for-test", "VV_MODE=http")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit code for -p with VV_MODE=http")
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 1 {
			t.Errorf("exit code = %d, want 1", exitErr.ExitCode())
		}
	}

	if !strings.Contains(stderr.String(), "incompatible") {
		t.Errorf("stderr = %q, expected to mention 'incompatible'", stderr.String())
	}
}

// --- Test: Missing config rejection for -p mode ---
// Verifies that `vv -p "hello"` with no API key and no config exits with code 1
// and prints a helpful error about missing configuration.
// Covers: design Test 3 -- missing config rejection.
func TestIntegration_Prompt_MissingConfigRejection(t *testing.T) {
	binary := buildVVBinary(t)
	defer func() { _ = os.Remove(binary) }()

	// Use a non-existent config path and clear all API key env vars.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "nonexistent.yaml")

	cmd := exec.Command(binary, "-p", "hello", "-config", configPath)
	// Remove all API key env vars to ensure no key is available.
	env := filterEnv(os.Environ(),
		"VV_LLM_API_KEY", "AI_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY")
	cmd.Env = env

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit code for missing config")
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 1 {
			t.Errorf("exit code = %d, want 1", exitErr.ExitCode())
		}
	}

	// Should mention missing config or API key.
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "config") && !strings.Contains(stderrStr, "API key") &&
		!strings.Contains(stderrStr, "configuration") {
		t.Errorf("stderr = %q, expected to mention config or API key", stderrStr)
	}
}

// --- Test: Real LLM prompt execution (requires API key) ---
// Performs a full end-to-end test: load real config, initialize, and run a prompt
// through cli.RunPrompt with the real dispatcher.
// Covers: design Test 1 -- basic prompt execution, Test 6 -- pipe-friendly output,
//
//	Test 7 -- stderr diagnostics, Test 8 -- task-complete summary.
func TestIntegration_Prompt_RealLLMExecution(t *testing.T) {
	configPath := configs.DefaultPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Skipf("config file %s not found, skipping integration test", configPath)
	}

	// Check for API key.
	if os.Getenv("VV_LLM_API_KEY") == "" {
		cfg, err := configs.Load(configPath, true)
		if err != nil || configs.NeedsSetup(cfg) {
			t.Skip("no LLM API key available, skipping integration test")
		}
	}

	initResult, err := setup.InitFromFile(configPath, true, nil)
	if err != nil {
		t.Fatalf("setup.InitFromFile: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer

	err = vvcli.RunPrompt(ctx, initResult.SetupResult.Dispatcher, "What is 2+2? Reply with just the number.", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	// Verify stdout has some output.
	stdoutStr := stdout.String()
	if len(strings.TrimSpace(stdoutStr)) == 0 {
		t.Error("stdout should contain agent response, but was empty")
	}

	// Verify stdout contains no ANSI escape codes (pipe-friendly).
	if strings.Contains(stdoutStr, "\x1b") {
		t.Errorf("stdout contains ANSI escape codes: %q", stdoutStr)
	}

	// Verify stderr has phase diagnostics.
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "[phase]") {
		t.Errorf("stderr missing [phase] diagnostics; got %q", stderrStr)
	}

	// Verify stderr has [done] summary.
	if !strings.Contains(stderrStr, "[done]") {
		t.Errorf("stderr missing [done] summary; got %q", stderrStr)
	}

	t.Logf("stdout: %s", stdoutStr)
	t.Logf("stderr: %s", stderrStr)
}

// --- Test: RunPrompt with error event on stream ---
// Verifies that EventError is written to stderr as [error] line.
// Covers: error event handling in diagnostic output.
func TestIntegration_Prompt_ErrorEventOnStderr(t *testing.T) {
	mock := &mockPromptStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventError, "test", "s1", schema.ErrorData{
				Message: "something went wrong in the pipeline",
			}))
			_ = send(schema.NewEvent(schema.EventTextDelta, "test", "s1", schema.TextDeltaData{Delta: "partial result"}))
			return nil
		},
	}

	var stdout, stderr bytes.Buffer

	err := vvcli.RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	if !strings.Contains(stderr.String(), "[error] something went wrong in the pipeline") {
		t.Errorf("stderr missing error event; got %q", stderr.String())
	}

	// Text should still appear on stdout.
	if !strings.Contains(stdout.String(), "partial result") {
		t.Errorf("stdout missing text delta; got %q", stdout.String())
	}
}

// --- Test: RunPrompt with token budget exhaustion warning ---
// Verifies that EventTokenBudgetExhausted produces a [warning] line on stderr.
// Covers: edge case of token budget exhaustion.
func TestIntegration_Prompt_TokenBudgetWarning(t *testing.T) {
	mock := &mockPromptStreamAgent{
		id: "test",
		producer: func(_ context.Context, send func(schema.Event) error) error {
			_ = send(schema.NewEvent(schema.EventTextDelta, "test", "s1", schema.TextDeltaData{Delta: "truncated"}))
			_ = send(schema.NewEvent(schema.EventTokenBudgetExhausted, "test", "s1", schema.TokenBudgetExhaustedData{
				Budget: 1000,
				Used:   1000,
			}))
			return nil
		},
	}

	var stdout, stderr bytes.Buffer

	err := vvcli.RunPrompt(context.Background(), mock, "test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	if !strings.Contains(stderr.String(), "[warning] token budget exhausted") {
		t.Errorf("stderr missing warning; got %q", stderr.String())
	}
}

// --- Helpers ---

// mockPromptStreamAgent implements agent.StreamAgent with a configurable producer
// for prompt integration testing.
type mockPromptStreamAgent struct {
	id       string
	producer func(ctx context.Context, send func(schema.Event) error) error
}

func (m *mockPromptStreamAgent) ID() string          { return m.id }
func (m *mockPromptStreamAgent) Name() string        { return m.id }
func (m *mockPromptStreamAgent) Description() string { return m.id }

func (m *mockPromptStreamAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{}, nil
}

func (m *mockPromptStreamAgent) RunStream(ctx context.Context, _ *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, 16, m.producer), nil
}

// buildVVBinary compiles the vv binary into a temp directory and returns its path.
// The caller is responsible for cleaning up the binary (defer os.Remove).
func buildVVBinary(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	binary := filepath.Join(dir, "vv")

	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = filepath.Join(projectRoot(), "vv")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build vv binary: %v\n%s", err, string(out))
	}

	return binary
}

// projectRoot returns the path to the vagents monorepo root.
func projectRoot() string {
	// Integration tests run from vv/integrations/, so go up two levels.
	// But since working directory can vary, use a known absolute path.
	return "/Users/hk/workspaces/github/vogo/vagents"
}

// filterEnv returns a copy of env with the named variables removed.
func filterEnv(env []string, remove ...string) []string {
	removeSet := make(map[string]bool, len(remove))
	for _, r := range remove {
		removeSet[r] = true
	}

	var result []string
	for _, e := range env {
		key := e
		if before, _, ok := strings.Cut(e, "="); ok {
			key = before
		}
		if !removeSet[key] {
			result = append(result, e)
		}
	}

	return result
}
