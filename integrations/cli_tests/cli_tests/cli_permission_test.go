package cli_tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

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
