package eval_tests

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/httpapis"
	vvmemory "github.com/vogo/vv/memories"
)

// mustRunRequest builds a minimal RunRequest with a single user message
// for use in table-driven eval cases. Split into a helper because we
// build RunRequests across several tests and inline literals get noisy.
func mustRunRequest(text string) *schema.RunRequest {
	return &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage(text)}}
}

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
		serveErr = httpapis.Serve(ctx, cfg, nil, stubAgent{}, nil, persistentMem, interactionStore, nil, nil, nil, nil)
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
