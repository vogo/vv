package cli_tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vv/agents"
	vvcli "github.com/vogo/vv/cli"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/tools"
)

// --- Test: WrapRegistryWithPermission wraps the registry ---
// Verifies that WrapRegistryWithPermission returns a wrapped registry that
// preserves tool listing and Get delegation.
func TestIntegration_CLI_WrapRegistryWithPermission(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModeDefault)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	// Should be a different object.
	if wrapped == reg {
		t.Error("WrapRegistryWithPermission should return a new wrapped registry")
	}

	// The wrapped registry should still expose the same tools.
	origList := reg.List()
	wrappedList := wrapped.List()
	if len(wrappedList) != len(origList) {
		t.Errorf("wrapped tool count = %d, original = %d", len(wrappedList), len(origList))
	}

	// The wrapped registry should delegate Get correctly.
	if _, ok := wrapped.Get("bash"); !ok {
		t.Error("wrapped registry should delegate Get for 'bash'")
	}
}

// --- Test: Permission executor approve flow ---
// Verifies that in default mode, the default confirmFn allows all (until wired to TUI).
func TestIntegration_CLI_PermissionExecutorApprove(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	// Default WrapRegistryWithPermission confirmFn allows all, simulating approval.
	ps := vvcli.NewPermissionState(configs.PermissionModeDefault)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	// Execute bash with a simple command -- default confirmFn approves.
	result, err := wrapped.Execute(context.Background(), "bash", `{"command":"echo hello"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.IsError {
		text := ""
		for _, p := range result.Content {
			if p.Type == "text" {
				text = p.Text
				break
			}
		}
		t.Errorf("expected successful execution, got error: %s", text)
	}
}

// --- Test: Permission executor auto-approves read-only tools ---
// Verifies that read-only tools execute without confirmation in default mode.
func TestIntegration_CLI_PermissionExecutorReadOnlyPassthrough(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModeDefault)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	// Execute read (read-only tool) -- should work without confirmation.
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test content"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := wrapped.Execute(context.Background(), "read", fmt.Sprintf(`{"file_path":%q}`, tmpFile))
	if err != nil {
		t.Fatalf("Execute read: %v", err)
	}

	if result.IsError {
		text := ""
		for _, p := range result.Content {
			if p.Type == "text" {
				text = p.Text
				break
			}
		}
		t.Errorf("expected successful passthrough, got error: %s", text)
	}
}

// --- Test: agents.Create accepts tool.ToolRegistry interface ---
// Verifies that agents.Create works with both the original registry and a wrapped one.
func TestIntegration_CLI_AgentsCreateWithWrappedRegistry(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{Message: aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent("test")}},
			},
		},
	}

	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		CLI:    configs.CLIConfig{PermissionMode: configs.PermissionModeDefault},
	}

	// Wrap registry (as main.go does).
	ps := vvcli.NewPermissionState(cfg.CLI.PermissionMode)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	cfg.Memory = configs.MemoryConfig{MaxConcurrency: 2}

	// Create agents with wrapped registry -- should work without error.
	allAgents := agents.Create(cfg, mock, wrapped, wrapped, wrapped, nil, nil)

	if allAgents.Coder.ID() != "coder" {
		t.Errorf("coder ID = %q, want %q", allAgents.Coder.ID(), "coder")
	}

	if allAgents.Chat.ID() != "chat" {
		t.Errorf("chat ID = %q, want %q", allAgents.Chat.ID(), "chat")
	}

	// Coder should still have tools.
	if len(allAgents.Coder.Tools()) != 6 {
		t.Errorf("coder tool count = %d, want 6", len(allAgents.Coder.Tools()))
	}

	// Chat should have no tools.
	if len(allAgents.Chat.Tools()) != 0 {
		t.Errorf("chat tool count = %d, want 0", len(allAgents.Chat.Tools()))
	}
}

// --- Test: WrapRegistryWithPermission preserves tool execution ---
// Verifies that wrapping a registry with permission mode still allows tool execution
// to pass through for read-only tools and preserves tool listing.
func TestIntegration_CLI_WrapRegistryWithPermissionPreservesExecution(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModeDefault)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	// Verify all 6 tools are still accessible via Get.
	for _, name := range []string{"bash", "read", "write", "edit", "glob", "grep"} {
		if _, ok := wrapped.Get(name); !ok {
			t.Errorf("wrapped registry missing tool %q", name)
		}
	}

	// Execute a read-only tool (read) -- should work directly in default mode.
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := wrapped.Execute(context.Background(), "read", fmt.Sprintf(`{"file_path":%q}`, tmpFile))
	if err != nil {
		t.Fatalf("Execute read: %v", err)
	}

	if result.IsError {
		t.Error("read should succeed without confirmation in default mode")
	}
}
