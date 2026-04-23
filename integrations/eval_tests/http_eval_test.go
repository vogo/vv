// Package eval_tests contains integration tests that exercise the full
// eval integration wiring: httpapis.Serve mounting the /v1/eval/run route
// based on cfg.Eval.Enabled, end-to-end JSON round-trips against a stub
// dispatcher, co-existence with the memory endpoints, and the requestID
// middleware. These intentionally avoid any real LLM traffic so CI can
// run them without VV_LLM_API_KEY.
package eval_tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	vageeval "github.com/vogo/vage/eval"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/httpapis"
	vvmemory "github.com/vogo/vv/memories"
)

// stubAgent is a deterministic dispatcher used by every HTTP integration
// test in this file: it echoes the last user message and reports a fixed
// non-zero usage profile so the latency+cost evaluators have inputs to
// score.
type stubAgent struct{}

func (stubAgent) ID() string          { return "dispatcher" }
func (stubAgent) Name() string        { return "Dispatcher" }
func (stubAgent) Description() string { return "stub dispatcher for eval integration tests" }

func (stubAgent) Run(_ context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	text := ""
	if len(req.Messages) > 0 {
		text = req.Messages[len(req.Messages)-1].Content.Text()
	}

	return &schema.RunResponse{
		Messages: []schema.Message{{
			Message: aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent(text)},
		}},
		Usage:    &aimodel.Usage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10},
		Duration: 5,
	}, nil
}

var _ agent.Agent = (*stubAgent)(nil)

// pickFreeAddr asks the kernel for an unused loopback TCP port so tests can
// run in parallel without colliding on a well-known port.
func pickFreeAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}

	addr := ln.Addr().String()
	_ = ln.Close()

	return addr
}

// serveConfig builds a minimally-valid Config with a freshly allocated
// loopback address for httpapis.Serve to bind against.
func serveConfig(t *testing.T, evalEnabled bool) *configs.Config {
	t.Helper()

	return &configs.Config{
		LLM:    configs.LLMConfig{Provider: "openai", Model: "test-model", APIKey: "test"},
		Server: configs.ServerConfig{Addr: pickFreeAddr(t)},
		Tools:  configs.ToolsConfig{BashTimeout: 5},
		Eval: configs.EvalConfig{
			Enabled:            evalEnabled,
			Concurrency:        1,
			TimeoutMs:          2000,
			Evaluators:         []string{"latency", "cost"},
			LatencyThresholdMs: 60000,
			CostBudgetTokens:   1000,
		},
	}
}

// servePersistentMem gives each test its own FileStore-backed persistent
// memory so the memory endpoints work end-to-end without colliding with
// any other test or the developer's home directory.
func servePersistentMem(t *testing.T) memory.Memory {
	t.Helper()

	store, err := vvmemory.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	return memory.NewPersistentMemoryWithStore(store)
}

// startServe launches httpapis.Serve in a goroutine and returns the bound
// base URL plus a shutdown function that cancels the server context and
// waits for Serve to return. Poll-wait loop keeps this reliable without
// making the tests rely on a fixed boot delay.
func startServe(t *testing.T, cfg *configs.Config) (baseURL string, shutdown func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	persistentMem := servePersistentMem(t)
	interactionStore := httpapis.NewInteractionStore(ctx, time.Second)

	var (
		serveErr error
		wg       sync.WaitGroup
	)

	wg.Go(func() {
		serveErr = httpapis.Serve(ctx, cfg, nil, stubAgent{}, nil, persistentMem, interactionStore, nil, nil, nil)
	})

	baseURL = "http://" + cfg.Server.Addr

	// Wait for the listener to come up before returning so tests don't
	// race with the boot of Serve; the server health response doesn't
	// matter here, only that a TCP connection can be established.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", cfg.Server.Addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()

			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	shutdown = func() {
		cancel()
		wg.Wait()

		if serveErr != nil && serveErr != http.ErrServerClosed {
			// Don't fail the test — Serve always returns nil on a clean
			// Shutdown — but surface any unexpected error for diagnostics.
			t.Logf("serve returned: %v", serveErr)
		}
	}

	return baseURL, shutdown
}

// --- AC-2.1 + AC-2.2: POST /v1/eval/run happy path ---
// Scenario: Eval enabled + well-formed body with two cases against the
// stub dispatcher returns 200 and an EvalReport with total=2 passed=2.
func TestIntegration_HTTPEval_HappyPath(t *testing.T) {
	cfg := serveConfig(t, true)
	baseURL, shutdown := startServe(t, cfg)
	defer shutdown()

	body := `{"cases":[{"id":"c1","input":"hi"},{"id":"c2","input":"bye"}]}`

	resp, err := http.Post(baseURL+"/v1/eval/run", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/eval/run: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body = %s", resp.StatusCode, string(raw))
	}

	var report vageeval.EvalReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("decode report: %v", err)
	}

	if report.TotalCases != 2 || report.PassedCases != 2 {
		t.Errorf("report = %+v, want total=2 passed=2", report)
	}

	if got := report.PassedCases + report.FailedCases + report.ErrorCases; got != report.TotalCases {
		t.Errorf("counts don't sum: passed=%d failed=%d error=%d total=%d",
			report.PassedCases, report.FailedCases, report.ErrorCases, report.TotalCases)
	}
}

// --- AC-2.5: eval disabled returns 404 for /v1/eval/run ---
// Scenario: When cfg.Eval.Enabled=false the route MUST NOT be registered
// at all (design §8: "closed feature is not exposed"). Posting to the
// endpoint must surface a 404, proving the mount is strictly gated.
func TestIntegration_HTTPEval_Disabled404(t *testing.T) {
	cfg := serveConfig(t, false)
	baseURL, shutdown := startServe(t, cfg)
	defer shutdown()

	resp, err := http.Post(baseURL+"/v1/eval/run", "application/json",
		strings.NewReader(`{"cases":[{"id":"c1","input":"hi"}]}`))
	if err != nil {
		t.Fatalf("POST /v1/eval/run: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 404 (route must not be mounted); body = %s", resp.StatusCode, string(raw))
	}
}

// --- AC-2.3: empty body returns 400 ---
// Scenario: body is not valid JSON. Handler must return 400 rather than
// 500 or 200-with-empty report.
func TestIntegration_HTTPEval_InvalidBody(t *testing.T) {
	cfg := serveConfig(t, true)
	baseURL, shutdown := startServe(t, cfg)
	defer shutdown()

	resp, err := http.Post(baseURL+"/v1/eval/run", "application/json", strings.NewReader("not-json"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// --- AC-2.3: empty cases array returns 400 ---
// Scenario: well-formed JSON with an empty `cases` array is a user error;
// we reject upfront instead of returning a vacuously successful report.
func TestIntegration_HTTPEval_EmptyCases(t *testing.T) {
	cfg := serveConfig(t, true)
	baseURL, shutdown := startServe(t, cfg)
	defer shutdown()

	resp, err := http.Post(baseURL+"/v1/eval/run", "application/json",
		strings.NewReader(`{"cases":[]}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// --- AC-2.3: single malformed case is counted as an error, not aborted ---
// Scenario: one case has input, the other is missing `input`. The good
// case should still run (passed=1) while the bad one is recorded in
// ErrorCases; TotalCases equals the sum so callers can gate on it.
func TestIntegration_HTTPEval_MalformedCaseIsErrorNotAbort(t *testing.T) {
	cfg := serveConfig(t, true)
	baseURL, shutdown := startServe(t, cfg)
	defer shutdown()

	body := `{"cases":[{"id":"good","input":"hi"},{"id":"bad"}]}`

	resp, err := http.Post(baseURL+"/v1/eval/run", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body = %s", resp.StatusCode, string(raw))
	}

	var report vageeval.EvalReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if report.TotalCases != 2 {
		t.Errorf("total = %d, want 2", report.TotalCases)
	}

	if report.PassedCases != 1 {
		t.Errorf("passed = %d, want 1", report.PassedCases)
	}

	if report.ErrorCases != 1 {
		t.Errorf("error = %d, want 1 (malformed case)", report.ErrorCases)
	}

	// The malformed case must show up in report.Results with its error
	// string populated, per design §5.1 and the code-review fix.
	hasBadResult := false

	for _, r := range report.Results {
		if r.CaseID == "bad" && r.Error != "" {
			hasBadResult = true
		}
	}

	if !hasBadResult {
		t.Errorf("expected a result for case 'bad' with an Error; results = %+v", report.Results)
	}
}

// --- AC-2.3: evaluator build error surfaces as 500 ---
// Scenario: cfg.Eval.Evaluators contains "contains" but ContainsKeywords
// is empty, which is a server-side misconfiguration. The handler should
// respond 500 up-front instead of attempting to score cases.
func TestIntegration_HTTPEval_EvaluatorBuildError(t *testing.T) {
	cfg := serveConfig(t, true)
	cfg.Eval.Evaluators = []string{"contains"}
	cfg.Eval.ContainsKeywords = nil

	baseURL, shutdown := startServe(t, cfg)
	defer shutdown()

	body := `{"cases":[{"id":"c1","input":"hi"}]}`

	resp, err := http.Post(baseURL+"/v1/eval/run", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusInternalServerError {
		raw, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 500, body = %s", resp.StatusCode, string(raw))
	}
}

// --- AC-2.4: /v1/eval/run coexists with memory endpoints ---
// Scenario: a single Serve(...) must expose both /v1/eval/run (eval) and
// /v1/memory/* without one masking the other. PUT a memory entry, GET it
// back, then post an eval run — all through the same server instance.
func TestIntegration_HTTPEval_CoexistsWithMemoryEndpoints(t *testing.T) {
	cfg := serveConfig(t, true)
	baseURL, shutdown := startServe(t, cfg)
	defer shutdown()

	// PUT /v1/memory/project/notes
	putReq, _ := http.NewRequest(http.MethodPut,
		baseURL+"/v1/memory/project/notes",
		strings.NewReader(`{"content":"hello from eval tests"}`))
	putReq.Header.Set("Content-Type", "application/json")

	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("PUT /v1/memory: %v", err)
	}

	_ = putResp.Body.Close()

	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT memory status = %d, want 200", putResp.StatusCode)
	}

	// GET /v1/memory/project/notes
	getResp, err := http.Get(baseURL + "/v1/memory/project/notes")
	if err != nil {
		t.Fatalf("GET /v1/memory: %v", err)
	}

	defer func() { _ = getResp.Body.Close() }()

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET memory status = %d, want 200", getResp.StatusCode)
	}

	// POST /v1/eval/run through the same server.
	evalResp, err := http.Post(baseURL+"/v1/eval/run", "application/json",
		strings.NewReader(`{"cases":[{"id":"c1","input":"hi"}]}`))
	if err != nil {
		t.Fatalf("POST /v1/eval/run: %v", err)
	}

	defer func() { _ = evalResp.Body.Close() }()

	if evalResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(evalResp.Body)
		t.Fatalf("eval status = %d, want 200, body = %s", evalResp.StatusCode, string(raw))
	}
}

// --- AC-2.1 + design §3: JSONL "messages" shorthand ---
// Scenario: the design allows passing either a string input or a full
// RunRequest-like object with `messages`. Verify the object form works
// end-to-end so that tools which build RunRequest objects directly (e.g.
// programmatic CI glue) are not forced into string shorthand.
func TestIntegration_HTTPEval_MessagesObjectInput(t *testing.T) {
	cfg := serveConfig(t, true)
	baseURL, shutdown := startServe(t, cfg)
	defer shutdown()

	payload := map[string]any{
		"cases": []map[string]any{
			{
				"id": "c1",
				"input": map[string]any{
					"messages": []map[string]any{
						{"role": "user", "content": "summarize"},
					},
				},
			},
		},
	}

	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		t.Fatalf("encode payload: %v", err)
	}

	resp, err := http.Post(baseURL+"/v1/eval/run", "application/json", buf)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body = %s", resp.StatusCode, string(raw))
	}

	var report vageeval.EvalReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if report.TotalCases != 1 || report.PassedCases != 1 {
		t.Errorf("report = %+v, want total=1 passed=1", report)
	}
}

// --- AC-2.4: requestIDMiddleware wraps the eval endpoint ---
// Scenario: the design routes /v1/eval/run under the same mux that
// requestIDMiddleware wraps. We can't inspect the context from outside
// the handler, but we can verify the handler returns a well-formed
// response (not a middleware crash / header-only response) under the
// middleware path — which would trip up if the route were registered on
// a different mux.
func TestIntegration_HTTPEval_ServedThroughMiddleware(t *testing.T) {
	cfg := serveConfig(t, true)
	baseURL, shutdown := startServe(t, cfg)
	defer shutdown()

	resp, err := http.Post(baseURL+"/v1/eval/run", "application/json",
		strings.NewReader(`{"cases":[{"id":"c1","input":"hello"}]}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// The Content-Type should be set by writeJSON, which is only reached
	// when the middleware chain let the request through correctly.
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json*", ct)
	}
}
