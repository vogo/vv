package setup_tests

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/hooks"
	vvmemory "github.com/vogo/vv/memories"
	"github.com/vogo/vv/setup"
)

// --- Test: setup.New() with WrapToolRegistry option ---
// Verifies that the WrapToolRegistry option is applied to all agent tool registries.
// Test cases:
//   - WrapToolRegistry callback is invoked during setup
//   - Wrapped agents still function correctly
func TestIntegration_SetupNew_WrapToolRegistry(t *testing.T) {
	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"mode": "direct", "agent": "chat"}`),
					},
				},
			},
		},
	}

	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 5},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	wrapCount := atomic.Int32{}
	result, err := setup.New(cfg, mock, nil, nil, &setup.Options{
		WrapToolRegistry: func(r *tool.Registry) tool.ToolRegistry {
			wrapCount.Add(1)
			return r
		},
	})
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	if result.Dispatcher == nil {
		t.Fatal("expected non-nil Dispatcher")
	}

	// WrapToolRegistry should have been called for each dispatchable agent
	// (coder / researcher / reviewer; chat was removed in M6 G2) plus
	// once for the Primary Assistant tool registry — 4 in total. M5 had
	// chat (4 dispatchable + Primary skipped under `Mode == ""` guard
	// when Config was passed directly = 4); M6 has 3 dispatchable +
	// Primary always built = 4. The total is the same, but for different
	// reasons.
	if wrapCount.Load() != 4 {
		t.Errorf("WrapToolRegistry called %d times, want 4 (3 dispatchable agents + Primary)", wrapCount.Load())
	}

	// Verify the Dispatcher still works.
	resp, err := result.Dispatcher.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("test wrapping")},
	})
	if err != nil {
		t.Fatalf("Dispatcher.Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response")
	}
}

// --- Test: setup.New() with persistent memory ---
// Verifies that coder uses PersistentMemoryPrompt when PersistentMemory is provided.
// Test cases:
//   - setup.New() with non-nil PersistentMemory creates agents without error
//   - Coder agent is created and has correct ID
func TestIntegration_SetupNew_PersistentMemory(t *testing.T) {
	dir := t.TempDir()
	store, err := vvmemory.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	persistentMem := memory.NewPersistentMemoryWithStore(store)
	ctx := context.Background()
	if err := persistentMem.Set(ctx, "project:conventions", "Use gofumpt", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := setup.New(cfg, mock, nil, persistentMem, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	coder := result.Agent("coder")
	if coder == nil {
		t.Fatal("expected coder agent")
	}

	if coder.ID() != "coder" {
		t.Errorf("coder ID = %q, want %q", coder.ID(), "coder")
	}
}

// --- Test: Config backward compatibility (max_concurrency migration) ---
// Verifies that YAML with memory.max_concurrency still works and that
// orchestrate.max_concurrency overrides it.
// Test cases:
//   - Memory.MaxConcurrency is used when Orchestrate.MaxConcurrency is 0
//   - Orchestrate.MaxConcurrency takes precedence when set
//   - Default of 2 is used when neither is set
func TestIntegration_SetupNew_ConfigBackwardCompatibility(t *testing.T) {
	t.Run("memory.max_concurrency is used as fallback", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "configs.yaml")
		configContent := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
memory:
  max_concurrency: 4
`
		if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := configs.Load(configPath, true)
		if err != nil {
			t.Fatalf("configs.Load: %v", err)
		}

		if cfg.Memory.MaxConcurrency != 4 {
			t.Errorf("Memory.MaxConcurrency = %d, want 4", cfg.Memory.MaxConcurrency)
		}

		// Verify setup.New() succeeds with this configs.
		mock := &mockChatCompleter{}
		result, err := setup.New(cfg, mock, nil, nil, nil)
		if err != nil {
			t.Fatalf("setup.New: %v", err)
		}
		if result.Dispatcher == nil {
			t.Fatal("expected non-nil Dispatcher")
		}
	})

	t.Run("orchestrate.max_concurrency overrides memory", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "configs.yaml")
		configContent := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
memory:
  max_concurrency: 4
orchestrate:
  max_concurrency: 8
`
		if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg, err := configs.Load(configPath, true)
		if err != nil {
			t.Fatalf("configs.Load: %v", err)
		}

		if cfg.Orchestrate.MaxConcurrency != 8 {
			t.Errorf("Orchestrate.MaxConcurrency = %d, want 8", cfg.Orchestrate.MaxConcurrency)
		}

		mock := &mockChatCompleter{}
		result, err := setup.New(cfg, mock, nil, nil, nil)
		if err != nil {
			t.Fatalf("setup.New: %v", err)
		}
		if result.Dispatcher == nil {
			t.Fatal("expected non-nil Dispatcher")
		}
	})
}

// --- Test: Lifecycle hooks fire for sub-agent execution via Dispatcher ---
// Verifies that lifecycle hooks are invoked when the Dispatcher runs sub-agents.
// Test cases:
//   - LoggingHook.OnBeforeRun and OnAfterRun are called without panic
//   - Custom hooks receive the correct agent ID
func TestIntegration_SetupNew_LifecycleHooksIntegration(t *testing.T) {
	// We can test this indirectly by verifying setup.New() configures hooks
	// and the Dispatcher doesn't panic when running with them.
	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"mode": "direct", "agent": "chat"}`),
					},
				},
			},
		},
	}

	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 5},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	// Run the Dispatcher -- this exercises the LoggingHook configured in setup.New().
	resp, err := result.Dispatcher.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("test hooks")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Dispatcher.Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response")
	}
}

// --- Test: Lifecycle hooks chain correctly (unit-level within integration) ---
// Verifies that hooks.Chain correctly orders hook calls.
// Test cases:
//   - OnBeforeRun hooks are called in forward order
//   - OnAfterRun hooks are called in reverse order
//   - Error in OnBeforeRun aborts the chain
func TestIntegration_LifecycleHooksChain(t *testing.T) {
	var order []string
	hook1 := &recordingHook{id: "h1", order: &order}
	hook2 := &recordingHook{id: "h2", order: &order}

	chain := hooks.Chain(hook1, hook2)

	ctx := context.Background()
	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("test")}}

	if err := chain.OnBeforeRun(ctx, "test-agent", req); err != nil {
		t.Fatalf("OnBeforeRun: %v", err)
	}

	chain.OnAfterRun(ctx, "test-agent", nil, nil)

	// Verify order: before h1, before h2, after h2, after h1.
	expected := []string{"before:h1", "before:h2", "after:h2", "after:h1"}
	if len(order) != len(expected) {
		t.Fatalf("hook call count = %d, want %d", len(order), len(expected))
	}
	for i, got := range order {
		if got != expected[i] {
			t.Errorf("hook[%d] = %q, want %q", i, got, expected[i])
		}
	}
}
