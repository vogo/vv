package prompt_cache_tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	largemodel "github.com/vogo/largemodel/model"
	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
)

// vendorCacheFloor is the typical minimum prefix length (tokens) before
// OpenAI automatic prefix caching and Anthropic prompt cache will store or
// read a block. Calls whose first prompt is shorter than this cannot
// distinguish "cache unused" from "prefix too small".
const vendorCacheFloor = 1024

type recordedCall struct {
	PromptCaching bool
	Tools         int
	Messages      int
	Usage         schema.Usage
}

// recordingCaller wraps a real largemodel.Caller and keeps per-call usage so
// tests can assert a cache hit on a later request without relying on the
// aggregated RunResponse.Usage (which mixes the write and the read).
type recordingCaller struct {
	inner largemodel.Caller
	mu    sync.Mutex
	calls []recordedCall
}

func (r *recordingCaller) Protocol() schema.Protocol { return r.inner.Protocol() }

func (r *recordingCaller) Call(ctx context.Context, req *largemodel.Request) (*largemodel.Response, error) {
	resp, err := r.inner.Call(ctx, req)

	rec := recordedCall{
		PromptCaching: req.PromptCaching,
		Tools:         len(req.Tools),
		Messages:      len(req.Messages),
	}
	if resp != nil {
		rec.Usage = resp.Usage
	}

	r.mu.Lock()
	r.calls = append(r.calls, rec)
	r.mu.Unlock()

	return resp, err
}

func (r *recordingCaller) CallStream(ctx context.Context, req *largemodel.Request) (*largemodel.Stream, error) {
	return r.inner.CallStream(ctx, req)
}

func (r *recordingCaller) snapshot() []recordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]recordedCall, len(r.calls))
	copy(out, r.calls)

	return out
}

// reset resets the recorded calls.
// nolint:unused
func (r *recordingCaller) reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}

type harness struct {
	rec     *recordingCaller
	agent   agent.Agent
	proto   schema.Protocol
	workDir string
}

func resolveAPIKey() string {
	for _, k := range []string{"VV_LLM_API_KEY", "AI_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}

	return ""
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

// cachePadding returns a stable-looking project-instructions block large
// enough to clear the vendor cache floor. The nonce keeps this process from
// inheriting another tenant's cached prefix while remaining identical across
// the two calls inside one test.
func cachePadding(nonce string) string {
	const block = "This paragraph exists only to lengthen the system prefix so the vendor prompt cache (KV / prefix cache) can store it. File tools require absolute paths. Do not invent project facts from this padding. "

	return "Prompt-cache probe nonce: " + nonce + "\n\n" + strings.Repeat(block, 80)
}

func resolveLLMConfig(t *testing.T) configs.LLMConfig {
	t.Helper()

	if key := resolveAPIKey(); key != "" {
		return configs.LLMConfig{
			Provider: envOr("VV_LLM_PROVIDER", "openai"),
			Model:    envOr("VV_LLM_MODEL", "gpt-4o-mini"),
			BaseURL:  os.Getenv("VV_LLM_BASE_URL"),
			APIKey:   key,
		}
	}

	path := configs.DefaultPath()
	cfg, err := configs.Load(path, true)
	if err != nil || configs.NeedsSetup(cfg) {
		t.Skip("no LLM API key in env or ~/.vv/vv.yaml; skipping prompt-cache live test")
	}

	return cfg.LLM
}

func bootResearcher(t *testing.T) *harness {
	t.Helper()

	llmCfg := resolveLLMConfig(t)

	workDir := t.TempDir()
	for i, name := range []string{"alpha.go", "beta.go", "gamma.go"} {
		body := fmt.Sprintf("package probe\n\nconst File%d = %q\n", i, name)
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	allowed := []string{workDir}
	cfg := &configs.Config{
		LLM:    llmCfg,
		Agents: configs.AgentsConfig{MaxIterations: 6},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools: configs.ToolsConfig{
			BashTimeout:    10,
			BashWorkingDir: workDir,
			AllowedDirs:    &allowed,
		},
		ProjectInstructions: cachePadding(t.Name() + "-" + fmt.Sprintf("%d", time.Now().UnixNano())),
	}

	inner, err := configs.NewLLMClient(cfg.LLM)
	if err != nil {
		t.Fatalf("NewLLMClient: %v", err)
	}

	rec := &recordingCaller{inner: inner}

	result, err := setup.New(cfg, rec, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	researcher := result.Agent("researcher")
	if researcher == nil {
		t.Fatal("researcher agent missing from setup")
	}

	return &harness{
		rec:     rec,
		agent:   researcher,
		proto:   rec.Protocol(),
		workDir: workDir,
	}
}

func (h *harness) run(t *testing.T, timeout time.Duration, req *schema.RunRequest) *schema.RunResponse {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	resp, err := h.agent.Run(ctx, req)
	if err != nil {
		t.Fatalf("researcher.Run: %v", err)
	}

	return resp
}

func logCalls(t *testing.T, calls []recordedCall) {
	t.Helper()

	for i, c := range calls {
		t.Logf("llm_call[%d] caching=%v tools=%d messages=%d prompt=%d completion=%d cache_read=%d cache_write=%d total=%d",
			i, c.PromptCaching, c.Tools, c.Messages,
			c.Usage.PromptTokens, c.Usage.CompletionTokens,
			c.Usage.CacheReadTokens, c.Usage.CacheWriteTokens, c.Usage.TotalTokens)
	}
}

// assertPromptCacheUsed fails when later LLM calls in this harness did not
// report a vendor cache read. The first call is the write (or a miss); hits
// are expected from the second call onward because system prompt + tool
// definitions are a stable prefix.
func assertPromptCacheUsed(t *testing.T, calls []recordedCall) {
	t.Helper()

	if len(calls) < 2 {
		t.Fatalf("need at least 2 LLM calls to observe a cache hit, got %d", len(calls))
	}

	for i, c := range calls {
		if !c.PromptCaching {
			t.Errorf("llm_call[%d] PromptCaching=false, want true (vv default-on)", i)
		}
	}

	first := calls[0].Usage
	if first.PromptTokens < vendorCacheFloor {
		t.Skipf("first-call prompt_tokens=%d is below the typical %d-token vendor cache floor; cannot assert a hit",
			first.PromptTokens, vendorCacheFloor)
	}

	var hits int

	for i := 1; i < len(calls); i++ {
		if calls[i].Usage.CacheReadTokens > 0 {
			hits++
		}
	}

	if hits == 0 {
		t.Errorf("no LLM call after the first reported CacheReadTokens>0; prompt/KV cache was not used. first prompt_tokens=%d cache_write=%d",
			first.PromptTokens, first.CacheWriteTokens)
	}
}
