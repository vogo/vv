package permission_tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	vvcli "github.com/vogo/vv/cli"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/tools"
)

// TestIntegration_Permission_AutoMode_ApprovesAllRealTools verifies that auto
// mode approves execution of all tool types (read-only, write, bash) using the
// real tool registry.
func TestIntegration_Permission_AutoMode_ApprovesAllRealTools(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModeAuto)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	// bash should be auto-approved.
	result, err := wrapped.Execute(context.Background(), "bash", `{"command":"echo hello"}`)
	if err != nil {
		t.Fatalf("Execute(bash): %v", err)
	}

	if result.IsError {
		t.Errorf("auto mode: bash should be approved, got error: %s", resultText(result))
	}

	// read (read-only) should be auto-approved.
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test content"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err = wrapped.Execute(context.Background(), "read", fmt.Sprintf(`{"file_path":%q}`, tmpFile))
	if err != nil {
		t.Fatalf("Execute(read): %v", err)
	}

	if result.IsError {
		t.Errorf("auto mode: read should be approved, got error: %s", resultText(result))
	}

	// glob (read-only) should be auto-approved.
	result, err = wrapped.Execute(context.Background(), "glob", fmt.Sprintf(`{"pattern":"*.txt","path":%q}`, t.TempDir()))
	if err != nil {
		t.Fatalf("Execute(glob): %v", err)
	}

	if result.IsError {
		t.Errorf("auto mode: glob should be approved, got error: %s", resultText(result))
	}
}

// TestIntegration_Permission_DefaultMode_ApprovesReadOnlyTools verifies that
// default mode auto-approves all read-only tools without confirmation, using the
// real tool registry.
func TestIntegration_Permission_DefaultMode_ApprovesReadOnlyTools(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModeDefault)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("content for grep test"), 0o644); err != nil {
		t.Fatal(err)
	}

	// read should pass through without confirmation.
	result, err := wrapped.Execute(context.Background(), "read", fmt.Sprintf(`{"file_path":%q}`, tmpFile))
	if err != nil {
		t.Fatalf("Execute(read): %v", err)
	}

	if result.IsError {
		t.Errorf("default mode: read should be auto-approved, got error: %s", resultText(result))
	}

	// glob should pass through without confirmation.
	result, err = wrapped.Execute(context.Background(), "glob", fmt.Sprintf(`{"pattern":"*.txt","path":%q}`, tmpDir))
	if err != nil {
		t.Fatalf("Execute(glob): %v", err)
	}

	if result.IsError {
		t.Errorf("default mode: glob should be auto-approved, got error: %s", resultText(result))
	}

	// grep should pass through without confirmation.
	result, err = wrapped.Execute(context.Background(), "grep", fmt.Sprintf(`{"pattern":"content","path":%q}`, tmpDir))
	if err != nil {
		t.Fatalf("Execute(grep): %v", err)
	}

	if result.IsError {
		t.Errorf("default mode: grep should be auto-approved, got error: %s", resultText(result))
	}
}

// TestIntegration_Permission_DefaultMode_ConfirmsWriteTools verifies that
// default mode requires confirmation for write/edit/bash tools. When the
// confirmFn denies, the tool call should be rejected.
func TestIntegration_Permission_DefaultMode_ConfirmsWriteTools(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModeDefault)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	// Set confirmFn to deny all tools.
	ps.SetConfirmFn(func(_ context.Context, _, _ string) (vvcli.PermissionAction, error) {
		return vvcli.PermissionDeny, nil
	})

	// bash should require confirmation and be denied.
	result, err := wrapped.Execute(context.Background(), "bash", `{"command":"echo hello"}`)
	if err != nil {
		t.Fatalf("Execute(bash): %v", err)
	}

	if !result.IsError {
		t.Error("default mode: bash should require confirmation and be denied")
	}

	if text := resultText(result); text != "Tool call rejected by user" {
		t.Errorf("rejection text = %q, want %q", text, "Tool call rejected by user")
	}

	// write should require confirmation and be denied.
	result, err = wrapped.Execute(context.Background(), "write", `{"file_path":"/tmp/nonexistent","content":"x"}`)
	if err != nil {
		t.Fatalf("Execute(write): %v", err)
	}

	if !result.IsError {
		t.Error("default mode: write should require confirmation and be denied")
	}

	// edit should require confirmation and be denied.
	result, err = wrapped.Execute(context.Background(), "edit", `{"file_path":"/tmp/nonexistent","old_string":"a","new_string":"b"}`)
	if err != nil {
		t.Fatalf("Execute(edit): %v", err)
	}

	if !result.IsError {
		t.Error("default mode: edit should require confirmation and be denied")
	}
}

// TestIntegration_Permission_AcceptEditsMode_ApprovesWriteAndEdit verifies that
// accept-edits mode auto-approves write and edit tools but still requires
// confirmation for bash, using the real tool registry.
func TestIntegration_Permission_AcceptEditsMode_ApprovesWriteAndEdit(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModeAcceptEdits)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	// Set confirmFn to deny -- only bash should reach it.
	confirmCalled := false
	ps.SetConfirmFn(func(_ context.Context, toolName, _ string) (vvcli.PermissionAction, error) {
		confirmCalled = true
		return vvcli.PermissionDeny, nil
	})

	// write should be auto-approved (no confirmation).
	tmpDir := t.TempDir()
	writeFile := filepath.Join(tmpDir, "write-test.txt")

	result, err := wrapped.Execute(context.Background(), "write",
		fmt.Sprintf(`{"file_path":%q,"content":"hello"}`, writeFile))
	if err != nil {
		t.Fatalf("Execute(write): %v", err)
	}

	if result.IsError {
		t.Errorf("accept-edits mode: write should be auto-approved, got error: %s", resultText(result))
	}

	if confirmCalled {
		t.Error("accept-edits mode: confirmFn should not be called for write")
	}

	// edit should be auto-approved (no confirmation).
	// First create a file to edit.
	editFile := filepath.Join(tmpDir, "edit-test.txt")
	if err := os.WriteFile(editFile, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}

	confirmCalled = false

	result, err = wrapped.Execute(context.Background(), "edit",
		fmt.Sprintf(`{"file_path":%q,"old_string":"old content","new_string":"new content"}`, editFile))
	if err != nil {
		t.Fatalf("Execute(edit): %v", err)
	}

	if result.IsError {
		t.Errorf("accept-edits mode: edit should be auto-approved, got error: %s", resultText(result))
	}

	if confirmCalled {
		t.Error("accept-edits mode: confirmFn should not be called for edit")
	}

	// bash should require confirmation (and be denied).
	confirmCalled = false

	result, err = wrapped.Execute(context.Background(), "bash", `{"command":"echo hello"}`)
	if err != nil {
		t.Fatalf("Execute(bash): %v", err)
	}

	if !result.IsError {
		t.Error("accept-edits mode: bash should require confirmation and be denied")
	}

	if !confirmCalled {
		t.Error("accept-edits mode: confirmFn should be called for bash")
	}
}

// TestIntegration_Permission_PlanMode_RejectsWriteTools verifies that plan mode
// rejects all non-read-only tools with a descriptive error message, using the
// real tool registry.
func TestIntegration_Permission_PlanMode_RejectsWriteTools(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModePlan)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	writeTools := []string{"write", "edit", "bash"}

	for _, toolName := range writeTools {
		result, err := wrapped.Execute(context.Background(), toolName, `{}`)
		if err != nil {
			t.Fatalf("Execute(%s): %v", toolName, err)
		}

		if !result.IsError {
			t.Errorf("plan mode: %s should be rejected", toolName)
		}

		text := resultText(result)
		if !strings.Contains(text, "not permitted in plan mode") {
			t.Errorf("plan mode: %s rejection text = %q, want to contain 'not permitted in plan mode'",
				toolName, text)
		}
	}
}

// TestIntegration_Permission_PlanMode_ApprovesReadOnlyTools verifies that plan
// mode allows read-only tools to execute, using the real tool registry.
func TestIntegration_Permission_PlanMode_ApprovesReadOnlyTools(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModePlan)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("plan mode test content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// read should be approved.
	result, err := wrapped.Execute(context.Background(), "read", fmt.Sprintf(`{"file_path":%q}`, tmpFile))
	if err != nil {
		t.Fatalf("Execute(read): %v", err)
	}

	if result.IsError {
		t.Errorf("plan mode: read should be approved, got error: %s", resultText(result))
	}

	// glob should be approved.
	result, err = wrapped.Execute(context.Background(), "glob", fmt.Sprintf(`{"pattern":"*.txt","path":%q}`, tmpDir))
	if err != nil {
		t.Fatalf("Execute(glob): %v", err)
	}

	if result.IsError {
		t.Errorf("plan mode: glob should be approved, got error: %s", resultText(result))
	}
}
