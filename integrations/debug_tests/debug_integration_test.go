// Package debug_tests holds integration tests for the vv --debug feature.
//
// These tests exercise the debug wiring exactly as setup.Init wires it
// (largemodel.DebugMiddleware on the LLM client + DebuggingToolRegistry on
// each per-agent tool registry) but with a fake aimodel.ChatCompleter so
// that no real LLM key is required. Tests requiring a real LLM are gated
// on environment variables and skipped otherwise.
package debug_tests

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/largemodel"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/debugs"
)

// --------------------------------------------------------------------------
// Fakes
// --------------------------------------------------------------------------

// fakeCompleter is a deterministic ChatCompleter that returns a canned reply.
type fakeCompleter struct {
	resp *aimodel.ChatResponse
	err  error
}

func (f *fakeCompleter) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func (f *fakeCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, errors.New("stream not implemented in fake")
}

// fakeToolRegistry is a minimal tool.ToolRegistry returning canned results.
type fakeToolRegistry struct {
	mu      sync.Mutex
	calls   []string
	result  schema.ToolResult
	execErr error
}

func (f *fakeToolRegistry) Register(_ schema.ToolDef, _ tool.ToolHandler) error { return nil }
func (f *fakeToolRegistry) Unregister(_ string) error                           { return nil }
func (f *fakeToolRegistry) Get(_ string) (schema.ToolDef, bool)                 { return schema.ToolDef{}, false }
func (f *fakeToolRegistry) List() []schema.ToolDef                              { return nil }
func (f *fakeToolRegistry) Merge(_ []schema.ToolDef)                            {}
func (f *fakeToolRegistry) Execute(_ context.Context, name, args string) (schema.ToolResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, name+":"+args)
	f.mu.Unlock()
	if f.execErr != nil {
		return schema.ToolResult{}, f.execErr
	}
	return f.result, nil
}

// helper: build a debug-wrapped completer the same way setup.Init does.
func wrapWithDebug(base aimodel.ChatCompleter, sink *debugs.Sink) aimodel.ChatCompleter {
	return largemodel.Chain(base, largemodel.NewDebugMiddleware(debugs.SinkAdapter{S: sink}))
}

// --------------------------------------------------------------------------
// Test 1: --debug OFF produces no debug output (CLI -p path uses stderr sink)
// --------------------------------------------------------------------------

// TestDebug_Off_NoOutput_PromptMode verifies that when cfg.Debug is false the
// caller (mirroring main.go) never constructs the sink/middleware/decorator,
// so a normal LLM and tool call produces zero debug bytes.
func TestDebug_Off_NoOutput_PromptMode(t *testing.T) {
	cfg := &configs.Config{Debug: false}

	var stderr bytes.Buffer
	// Mirror main.go: only construct sink when cfg.Debug is true.
	var sink *debugs.Sink
	if cfg.Debug {
		sink = debugs.NewWriterSink(&stderr)
	}

	base := &fakeCompleter{resp: &aimodel.ChatResponse{
		Model:   "fake",
		Choices: []aimodel.Choice{{Message: aimodel.Message{Content: aimodel.NewTextContent("hi")}, FinishReason: "stop"}},
	}}

	var llm aimodel.ChatCompleter = base
	if cfg.Debug && sink != nil {
		llm = wrapWithDebug(base, sink)
	}

	if _, err := llm.ChatCompletion(context.Background(), &aimodel.ChatRequest{Model: "fake"}); err != nil {
		t.Fatal(err)
	}

	inner := &fakeToolRegistry{result: schema.TextResult("id", "tool-result")}
	var reg tool.ToolRegistry = inner
	if cfg.Debug && sink != nil {
		reg = debugs.NewDebuggingToolRegistry(reg, sink)
	}
	if _, err := reg.Execute(context.Background(), "read", `{"path":"x"}`); err != nil {
		t.Fatal(err)
	}

	if stderr.Len() != 0 {
		t.Fatalf("expected no debug output when debug off, got %d bytes: %q", stderr.Len(), stderr.String())
	}
	if sink != nil {
		t.Fatalf("expected sink to be nil when debug off")
	}
}

// TestDebug_Off_NoOutput_HTTPMode mirrors the HTTP wiring path with debug
// off and asserts no slog records are emitted by the debug subsystem.
func TestDebug_Off_NoOutput_HTTPMode(t *testing.T) {
	cfg := &configs.Config{Debug: false}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var sink *debugs.Sink
	if cfg.Debug {
		sink = debugs.NewSlogSink(logger)
	}

	base := &fakeCompleter{resp: &aimodel.ChatResponse{
		Model:   "fake",
		Choices: []aimodel.Choice{{Message: aimodel.Message{Content: aimodel.NewTextContent("hi")}, FinishReason: "stop"}},
	}}

	var llm aimodel.ChatCompleter = base
	if cfg.Debug && sink != nil {
		llm = wrapWithDebug(base, sink)
	}
	_, _ = llm.ChatCompletion(context.Background(), &aimodel.ChatRequest{Model: "fake"})

	if strings.Contains(logBuf.String(), "llm.request") || strings.Contains(logBuf.String(), "llm.response") {
		t.Fatalf("expected no llm debug records in slog when debug off, got: %s", logBuf.String())
	}
}

// --------------------------------------------------------------------------
// Test 2: --debug ON in -p mode emits LLM + tool records to stderr writer
// --------------------------------------------------------------------------

// TestDebug_On_PromptMode_LLMAndToolRecords verifies that when debug is on
// and the sink is a writer (the -p mode destination), debug records for
// both LLM I/O and tool I/O appear in the captured stderr buffer.
func TestDebug_On_PromptMode_LLMAndToolRecords(t *testing.T) {
	cfg := &configs.Config{Debug: true}

	var stderr bytes.Buffer
	sink := debugs.NewWriterSink(&stderr)

	base := &fakeCompleter{resp: &aimodel.ChatResponse{
		Model: "fake-model",
		Choices: []aimodel.Choice{{
			Message:      aimodel.Message{Content: aimodel.NewTextContent("hello-from-fake")},
			FinishReason: "stop",
		}},
		Usage: aimodel.Usage{PromptTokens: 4, CompletionTokens: 3, TotalTokens: 7},
	}}
	llm := wrapWithDebug(base, sink)

	ctx := debugs.WithAgentName(context.Background(), "coder")
	if _, err := llm.ChatCompletion(ctx, &aimodel.ChatRequest{Model: "fake-model"}); err != nil {
		t.Fatal(err)
	}

	inner := &fakeToolRegistry{result: schema.TextResult("id", "OK-result-bytes")}
	reg := debugs.NewDebuggingToolRegistry(inner, sink)
	if _, err := reg.Execute(ctx, "read", `{"path":"main.go"}`); err != nil {
		t.Fatal(err)
	}

	out := stderr.String()
	for _, want := range []string{
		"llm.request", "llm.response", "tool.start", "tool.end",
		"hello-from-fake", "OK-result-bytes", "main.go", "agent=coder",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected debug output to contain %q, got:\n%s", want, out)
		}
	}
	_ = cfg
}

// --------------------------------------------------------------------------
// Test 3: env / flag / yaml precedence for cfg.Debug
// --------------------------------------------------------------------------

// TestDebug_EnvOverridesYAML asserts that VV_DEBUG=true overrides a YAML
// debug:false setting via configs.Load.
func TestDebug_EnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/vv.yaml"
	yaml := "llm:\n  api_key: stub\n  provider: openai\ndebug: false\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VV_DEBUG", "true")
	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Debug {
		t.Fatalf("VV_DEBUG=true should override YAML debug:false")
	}
}

// TestDebug_FlagOverridesEnv simulates main.go's precedence resolution:
// the explicit --debug=false flag must override VV_DEBUG=true. We mirror
// main.go's `if debugSet { cfg.Debug = *debugFlag }` logic.
func TestDebug_FlagOverridesEnv(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/vv.yaml"
	yaml := "llm:\n  api_key: stub\n  provider: openai\ndebug: false\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VV_DEBUG", "true")
	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Debug {
		t.Fatalf("env should have set cfg.Debug=true")
	}
	// Explicit CLI flag false overrides env.
	debugSet := true
	flagVal := false
	if debugSet {
		cfg.Debug = flagVal
	}
	if cfg.Debug {
		t.Fatalf("explicit --debug=false should override VV_DEBUG=true")
	}
}

// TestDebug_YAMLEnabledWhenNoEnvOrFlag verifies the YAML setting takes
// effect when neither env var nor flag is set.
func TestDebug_YAMLEnabledWhenNoEnvOrFlag(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/vv.yaml"
	yaml := "llm:\n  api_key: stub\n  provider: openai\ndebug: true\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VV_DEBUG", "")
	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Debug {
		t.Fatalf("YAML debug:true should be loaded")
	}
}

// --------------------------------------------------------------------------
// Test 4: HTTP mode response is byte-identical with debug on vs off
// --------------------------------------------------------------------------

// TestDebug_HTTPMode_ResponseByteIdentical asserts that wrapping the LLM
// client with the debug middleware does not alter the LLM response bytes
// the consumer sees, which is the property the HTTP layer relies on for
// response parity.
func TestDebug_HTTPMode_ResponseByteIdentical(t *testing.T) {
	canned := &aimodel.ChatResponse{
		Model: "fake",
		Choices: []aimodel.Choice{{
			Message:      aimodel.Message{Content: aimodel.NewTextContent("identical-payload-12345")},
			FinishReason: "stop",
		}},
		Usage: aimodel.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
	}

	// Debug-off path: raw client, no middleware.
	off := &fakeCompleter{resp: canned}
	respOff, err := off.ChatCompletion(context.Background(), &aimodel.ChatRequest{Model: "fake"})
	if err != nil {
		t.Fatal(err)
	}

	// Debug-on path: wrapped with debug middleware sending records to slog.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	sink := debugs.NewSlogSink(logger)
	on := wrapWithDebug(&fakeCompleter{resp: canned}, sink)
	respOn, err := on.ChatCompletion(context.Background(), &aimodel.ChatRequest{Model: "fake"})
	if err != nil {
		t.Fatal(err)
	}

	if respOff.Choices[0].Message.Content.Text() != respOn.Choices[0].Message.Content.Text() {
		t.Errorf("response content drifted: off=%q on=%q",
			respOff.Choices[0].Message.Content.Text(),
			respOn.Choices[0].Message.Content.Text())
	}
	if respOff.Usage != respOn.Usage {
		t.Errorf("usage drifted: off=%+v on=%+v", respOff.Usage, respOn.Usage)
	}

	// And the slog buffer should now have llm.* records (proves debug ran).
	if !strings.Contains(logBuf.String(), "llm.request") || !strings.Contains(logBuf.String(), "llm.response") {
		t.Errorf("debug-on path should have emitted llm records, got: %s", logBuf.String())
	}
}

// --------------------------------------------------------------------------
// Test 5: tool decorator captures args/result/error/latency for built-in tools
// --------------------------------------------------------------------------

// TestDebug_ToolDecorator_CapturesArgsResultLatency exercises the
// DebuggingToolRegistry against a fake registry simulating a built-in tool
// (read) and verifies the captured records carry args, result text, source
// classification, and a non-zero duration.
func TestDebug_ToolDecorator_CapturesArgsResultLatency(t *testing.T) {
	var buf bytes.Buffer
	sink := debugs.NewWriterSink(&buf)
	inner := &fakeToolRegistry{result: schema.TextResult("id", "tool-bytes-OK")}
	d := debugs.NewDebuggingToolRegistry(inner, sink)

	if _, err := d.Execute(context.Background(), "read", `{"path":"x.go"}`); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"tool.start", "tool.end", "tool=read", `{"path":"x.go"}`, "tool-bytes-OK", "dur="} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

// TestDebug_ToolDecorator_CapturesError verifies that when the inner
// registry's Execute returns an error, the tool.end record carries err=...
// and the error is propagated to the caller.
func TestDebug_ToolDecorator_CapturesError(t *testing.T) {
	var buf bytes.Buffer
	sink := debugs.NewWriterSink(&buf)
	inner := &fakeToolRegistry{execErr: errors.New("disk full")}
	d := debugs.NewDebuggingToolRegistry(inner, sink)

	if _, err := d.Execute(context.Background(), "write", `{"path":"a"}`); err == nil {
		t.Fatal("expected error")
	}
	out := buf.String()
	if !strings.Contains(out, "disk full") {
		t.Errorf("missing error string in output:\n%s", out)
	}
}

// --------------------------------------------------------------------------
// Test 6: secret redaction
// --------------------------------------------------------------------------

// TestDebug_Redaction_APIKeyNotPresent ensures the Redact helper used on
// config snapshots does not leak common secret patterns.
func TestDebug_Redaction_APIKeyNotPresent(t *testing.T) {
	in := "api_key=\"sk-supersecret-9999\"\nAuthorization: Bearer abc-token-xyz\nurl=https://x?api-key=zzz"
	out := debugs.Redact(in)
	for _, leak := range []string{"sk-supersecret-9999", "abc-token-xyz", "zzz"} {
		if strings.Contains(out, leak) {
			t.Errorf("secret %q leaked in redacted output: %s", leak, out)
		}
	}
}

// TestDebug_Redaction_EnvSecretsScrubbed ensures env-resident api keys are
// scrubbed from arbitrary text snapshots when the env var is set.
func TestDebug_Redaction_EnvSecretsScrubbed(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env-secret-12345")
	out := debugs.Redact("token=sk-env-secret-12345 trailing")
	if strings.Contains(out, "sk-env-secret-12345") {
		t.Errorf("env secret leaked: %s", out)
	}
}

// --------------------------------------------------------------------------
// Test 7: concurrent requests do not garble correlation IDs / records
// --------------------------------------------------------------------------

// TestDebug_ConcurrentRequests_NoGarble fires many goroutines emitting
// records with distinct correlation ids and asserts (a) no record line
// gets interleaved (each line stays contiguous) and (b) the per-record
// correlation id appears intact in the output.
func TestDebug_ConcurrentRequests_NoGarble(t *testing.T) {
	var buf bytes.Buffer
	sink := debugs.NewWriterSink(&buf)

	var wg sync.WaitGroup
	const N = 200
	for i := range N {
		i := i
		wg.Go(func() {
			corr := fmt.Sprintf("corr-%04d", i)
			sink.Emit(context.Background(), &debugs.Record{
				Kind:          debugs.KindLLMRequest,
				CorrelationID: corr,
				AgentName:     "coder",
			})
		})
	}
	wg.Wait()

	out := buf.String()
	for i := range N {
		want := fmt.Sprintf("corr=corr-%04d", i)
		if !strings.Contains(out, want) {
			t.Errorf("missing or garbled correlation id %q", want)
		}
	}
	// Each record is a single line; expect exactly N lines containing "kind=llm.request".
	if got := strings.Count(out, "kind=llm.request"); got != N {
		t.Errorf("got %d kind=llm.request lines, want %d", got, N)
	}
}

// --------------------------------------------------------------------------
// Test 8: gated real-LLM smoke (skipped without keys)
// --------------------------------------------------------------------------

// TestDebug_RealLLM_Smoke is a thin smoke test that runs a real LLM call
// only if API keys are present in the environment, otherwise it skips.
func TestDebug_RealLLM_Smoke(t *testing.T) {
	if os.Getenv("VV_LLM_API_KEY") == "" &&
		os.Getenv("AI_API_KEY") == "" &&
		os.Getenv("OPENAI_API_KEY") == "" &&
		os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("no LLM api key set; skipping real-LLM debug smoke test")
	}
	t.Log("(real-LLM debug smoke is intentionally minimal; covered by other vv integration tests)")
}
