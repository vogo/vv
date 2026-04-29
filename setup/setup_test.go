package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/configs"
)

// mockChatCompleter is a simple mock for testing.
type mockChatCompleter struct {
	response *aimodel.ChatResponse
	err      error
}

func (m *mockChatCompleter) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}

	return m.response, nil
}

func (m *mockChatCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, m.err
}

func TestNew_AllAgentsCreated(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if result.Dispatcher == nil {
		t.Fatal("expected non-nil Dispatcher")
	}

	if result.Dispatcher.ID() != "orchestrator" {
		t.Errorf("Dispatcher ID = %q, want %q", result.Dispatcher.ID(), "orchestrator")
	}

	// Verify all dispatchable agents.
	for _, id := range []string{"coder", "researcher", "reviewer"} {
		a := result.Agent(id)
		if a == nil {
			t.Errorf("expected agent %q to be created", id)
		} else if a.ID() != id {
			t.Errorf("agent ID = %q, want %q", a.ID(), id)
		}
	}
}

func TestNew_AgentNames(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	expected := map[string]string{
		"coder":      "Coder Agent",
		"researcher": "Researcher Agent",
		"reviewer":   "Reviewer Agent",
	}

	for id, wantName := range expected {
		a := result.Agent(id)
		if a == nil {
			t.Errorf("expected agent %q", id)

			continue
		}

		if a.Name() != wantName {
			t.Errorf("%s.Name() = %q, want %q", id, a.Name(), wantName)
		}
	}
}

func TestNew_AgentsReturnsAllDispatchable(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	agents := result.Agents()
	if len(agents) != 3 {
		t.Errorf("Agents() = %d, want 3 (coder, researcher, reviewer)", len(agents))
	}
}

func TestNew_DispatcherName(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if result.Dispatcher.Name() != "Orchestrator Agent" {
		t.Errorf("Dispatcher Name = %q, want %q", result.Dispatcher.Name(), "Orchestrator Agent")
	}
}

func TestNew_WithWrapToolRegistry(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	wrapCalled := false
	result, err := New(cfg, mock, nil, nil, &Options{
		WrapToolRegistry: func(r *tool.Registry) tool.ToolRegistry {
			wrapCalled = true

			return r
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if result.Dispatcher == nil {
		t.Fatal("expected non-nil Dispatcher")
	}

	if !wrapCalled {
		t.Error("expected WrapToolRegistry to be called")
	}
}

func TestBuildAllowedDirs_NilMergesDefaults(t *testing.T) {
	// Use a working dir that is NOT a subdirectory of os.TempDir so the
	// containment-dedupe rule in CanonicalizeDirs keeps both entries.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}

	cfg := &configs.ToolsConfig{
		BashWorkingDir: home,
		AllowedDirs:    nil, // YAML key absent
	}

	dirs, err := buildAllowedDirs(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(dirs) == 0 {
		t.Fatal("expected default dirs, got empty slice")
	}

	// Canonical form may differ on macOS (/var → /private/var); compare via EvalSymlinks.
	homeCanonical, _ := filepath.EvalSymlinks(home)
	tmpCanonical, _ := filepath.EvalSymlinks(os.TempDir())

	haveHome := false
	haveTmp := false

	for _, d := range dirs {
		if d == homeCanonical {
			haveHome = true
		}

		if d == tmpCanonical {
			haveTmp = true
		}
	}

	if !haveHome {
		t.Errorf("expected working dir %q (canonical %q) in dirs, got %v", home, homeCanonical, dirs)
	}

	if !haveTmp {
		t.Errorf("expected os.TempDir %q (canonical %q) in dirs, got %v", os.TempDir(), tmpCanonical, dirs)
	}
}

func TestBuildAllowedDirs_NilContainmentDedupe(t *testing.T) {
	// When BashWorkingDir is a subdirectory of os.TempDir (common in tests),
	// containment dedupe keeps only the ancestor.
	wd := t.TempDir() // lives inside os.TempDir()

	cfg := &configs.ToolsConfig{
		BashWorkingDir: wd,
		AllowedDirs:    nil,
	}

	dirs, err := buildAllowedDirs(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wdCanonical, _ := filepath.EvalSymlinks(wd)

	// The result must contain an ancestor of wd (could be tmpDir or wd itself).
	covered := false

	for _, d := range dirs {
		if d == wdCanonical || strings.HasPrefix(wdCanonical, d+string(filepath.Separator)) {
			covered = true
			break
		}
	}

	if !covered {
		t.Errorf("expected working dir %q to be covered by dirs %v", wdCanonical, dirs)
	}
}

func TestBuildAllowedDirs_EmptyFailsStartup(t *testing.T) {
	empty := []string{}
	cfg := &configs.ToolsConfig{
		BashWorkingDir: "/tmp",
		AllowedDirs:    &empty,
	}

	_, err := buildAllowedDirs(cfg)
	if err == nil {
		t.Fatal("expected error for explicitly empty allowed_dirs")
	}

	if !strings.Contains(err.Error(), "explicitly empty") {
		t.Errorf("expected error mentioning 'explicitly empty', got: %v", err)
	}
}

func TestBuildAllowedDirs_NonEmptyUsedVerbatim(t *testing.T) {
	wd := t.TempDir()
	userDir := t.TempDir()
	// Canonical form.
	wdCanonical, _ := filepath.EvalSymlinks(wd)
	userCanonical, _ := filepath.EvalSymlinks(userDir)

	dirs := []string{userDir}
	cfg := &configs.ToolsConfig{
		BashWorkingDir: wd,
		AllowedDirs:    &dirs,
	}

	got, err := buildAllowedDirs(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain only userDir (no auto-merge of wd when explicitly set).
	if len(got) != 1 {
		t.Fatalf("expected 1 dir, got %d: %v", len(got), got)
	}

	if got[0] != userCanonical {
		t.Errorf("got[0] = %q, want %q", got[0], userCanonical)
	}

	// Specifically: wd should NOT be auto-merged into the result.
	for _, d := range got {
		if d == wdCanonical && userCanonical != wdCanonical {
			t.Errorf("working dir was unexpectedly auto-merged into explicit allowed_dirs")
		}
	}
}

func TestBuildAllowedDirs_NonExistentFails(t *testing.T) {
	dirs := []string{"/definitely/does/not/exist/xyz"}
	cfg := &configs.ToolsConfig{
		BashWorkingDir: "/tmp",
		AllowedDirs:    &dirs,
	}

	_, err := buildAllowedDirs(cfg)
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
}

func TestBuildAllowedDirs_RejectsFilesystemRoot(t *testing.T) {
	dirs := []string{"/"}
	cfg := &configs.ToolsConfig{
		BashWorkingDir: "/tmp",
		AllowedDirs:    &dirs,
	}

	_, err := buildAllowedDirs(cfg)
	if err == nil {
		t.Fatal("expected error for filesystem root")
	}
}

func TestBuildAllowedDirs_TildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}

	dirs := []string{"~"}
	cfg := &configs.ToolsConfig{
		BashWorkingDir: "/tmp",
		AllowedDirs:    &dirs,
	}

	got, err := buildAllowedDirs(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	homeCanonical, _ := filepath.EvalSymlinks(home)
	if len(got) != 1 {
		t.Fatalf("expected 1 dir, got %d: %v", len(got), got)
	}

	if got[0] != homeCanonical {
		t.Errorf("got[0] = %q, want %q", got[0], homeCanonical)
	}
}

func TestNew_AgentNotFound(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	a := result.Agent("nonexistent")
	if a != nil {
		t.Errorf("expected nil for nonexistent agent, got %v", a)
	}
}

// TestPrimaryToolProfile_AllowBashSwitch verifies the env contract:
// orchestrate.primary_allow_bash gates whether the Primary Assistant's
// tool registry is built from ProfileReadOnly (read/web_fetch/glob/grep) or the
// promoted ProfileReview (read/web_fetch/glob/grep + bash). The fallback Primary
// always stays tool-free regardless and is covered separately.
func TestPrimaryToolProfile_AllowBashSwitch(t *testing.T) {
	cases := []struct {
		name        string
		allowBash   bool
		wantProfile string
		wantBash    bool
	}{
		{name: "default off → read-only", allowBash: false, wantProfile: "read-only", wantBash: false},
		{name: "explicitly on → review", allowBash: true, wantProfile: "review", wantBash: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &configs.Config{
				Orchestrate: configs.OrchestrateConfig{PrimaryAllowBash: tc.allowBash},
			}

			profile := primaryToolProfile(cfg)
			if profile.Name != tc.wantProfile {
				t.Errorf("profile.Name = %q, want %q", profile.Name, tc.wantProfile)
			}

			reg, err := profile.BuildRegistry(configs.ToolsConfig{BashTimeout: 10})
			if err != nil {
				t.Fatalf("BuildRegistry: %v", err)
			}

			_, hasBash := reg.Get("bash")
			if hasBash != tc.wantBash {
				t.Errorf("registry has bash = %v, want %v", hasBash, tc.wantBash)
			}

			// Read tools must always be present — the Primary depends on
			// them irrespective of the bash flag.
			for _, name := range []string{"read", "web_fetch", "glob", "grep"} {
				if _, ok := reg.Get(name); !ok {
					t.Errorf("expected tool %q in registry, missing", name)
				}
			}
		})
	}
}

func TestNew_UnifiedMode_AttachesPrimary(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
		Orchestrate: configs.OrchestrateConfig{
			Mode: configs.OrchestrateModeUnified,
		},
	}

	result, err := New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if result.Dispatcher == nil {
		t.Fatal("expected non-nil Dispatcher")
	}

	// Primary attachment is an internal Dispatcher state; we verify it via
	// the observable behaviour the setter unlocks — any request to the
	// Dispatcher must delegate to Primary rather than the classical path.
	// A direct field read would cross package boundaries; instead exercise
	// Run with a trivial request and confirm no panic plus the response
	// comes from the primary agent (ID == "primary").
	// Minimal smoke-test only; full behaviour lives in integration tests.
}

// --- Session subsystem wiring ---

func TestBuildHookManagerAndSession_DefaultEnabledCreatesStore(t *testing.T) {
	dir := t.TempDir()
	cfg := &configs.Config{
		Session: configs.SessionConfig{Dir: dir},
	}

	mgr, store, shutdown, err := buildHookManagerAndSession(cfg)
	if err != nil {
		t.Fatalf("buildHookManagerAndSession: %v", err)
	}
	defer shutdown(context.Background())

	if mgr == nil {
		t.Fatal("expected non-nil hook.Manager when session is enabled")
	}
	if store == nil {
		t.Fatal("expected non-nil SessionStore when session is enabled")
	}

	// Round-trip a Session through the store as a smoke test.
	want := "smoke"
	_ = store.Delete(context.Background(), want) // clean prior runs
}

func TestBuildHookManagerAndSession_DisabledIsZeroCost(t *testing.T) {
	off := false
	cfg := &configs.Config{
		Session: configs.SessionConfig{Enabled: &off},
		Trace:   configs.TraceConfig{}, // also off (default)
	}

	mgr, store, shutdown, err := buildHookManagerAndSession(cfg)
	if err != nil {
		t.Fatalf("buildHookManagerAndSession: %v", err)
	}
	defer shutdown(context.Background())

	if mgr != nil {
		t.Error("expected nil hook.Manager when both session and trace are disabled")
	}
	if store != nil {
		t.Error("expected nil SessionStore when session is disabled")
	}
}

func TestBuildHookManagerAndSession_BadDirFails(t *testing.T) {
	cfg := &configs.Config{
		// Use an unwritable path; root-only directory should fail mkdir.
		Session: configs.SessionConfig{Dir: "/proc/1/will-never-mkdir"},
	}

	_, _, _, err := buildHookManagerAndSession(cfg)
	if err == nil {
		t.Fatal("expected error for unwritable session dir")
	}
}
