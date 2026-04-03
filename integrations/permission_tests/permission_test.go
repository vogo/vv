package permission_tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vage/tool/askuser"
	"github.com/vogo/vage/tool/bash"
	"github.com/vogo/vage/tool/edit"
	"github.com/vogo/vage/tool/glob"
	"github.com/vogo/vage/tool/grep"
	"github.com/vogo/vage/tool/read"
	"github.com/vogo/vage/tool/write"
	vvcli "github.com/vogo/vv/cli"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/tools"
)

// resultText extracts the first text content part from a ToolResult.
func resultText(r schema.ToolResult) string {
	for _, p := range r.Content {
		if p.Type == "text" {
			return p.Text
		}
	}

	return ""
}

// =============================================================================
// Section 6.5: ToolDef ReadOnly integration tests
// Verifies that each tool's ToolDef() returns the expected ReadOnly value
// using the real tool packages (not mocks).
// =============================================================================

// TestIntegration_ToolDef_ReadOnly_ReadTool verifies that the read tool's
// ToolDef declares ReadOnly: true.
func TestIntegration_ToolDef_ReadOnly_ReadTool(t *testing.T) {
	rt := read.New()
	def := rt.ToolDef()

	if !def.ReadOnly {
		t.Errorf("read ToolDef().ReadOnly = false, want true")
	}
}

// TestIntegration_ToolDef_ReadOnly_GlobTool verifies that the glob tool's
// ToolDef declares ReadOnly: true.
func TestIntegration_ToolDef_ReadOnly_GlobTool(t *testing.T) {
	gt := glob.New()
	def := gt.ToolDef()

	if !def.ReadOnly {
		t.Errorf("glob ToolDef().ReadOnly = false, want true")
	}
}

// TestIntegration_ToolDef_ReadOnly_GrepTool verifies that the grep tool's
// ToolDef declares ReadOnly: true.
func TestIntegration_ToolDef_ReadOnly_GrepTool(t *testing.T) {
	gt := grep.New()
	def := gt.ToolDef()

	if !def.ReadOnly {
		t.Errorf("grep ToolDef().ReadOnly = false, want true")
	}
}

// TestIntegration_ToolDef_ReadOnly_AskUserTool verifies that the ask_user tool's
// ToolDef declares ReadOnly: true.
func TestIntegration_ToolDef_ReadOnly_AskUserTool(t *testing.T) {
	at := askuser.New(askuser.NonInteractiveInteractor{})
	def := at.ToolDef()

	if !def.ReadOnly {
		t.Errorf("ask_user ToolDef().ReadOnly = false, want true")
	}
}

// TestIntegration_ToolDef_ReadOnly_WriteTool verifies that the write tool's
// ToolDef declares ReadOnly: false (default, non-read-only).
func TestIntegration_ToolDef_ReadOnly_WriteTool(t *testing.T) {
	wt := write.New()
	def := wt.ToolDef()

	if def.ReadOnly {
		t.Errorf("write ToolDef().ReadOnly = true, want false")
	}
}

// TestIntegration_ToolDef_ReadOnly_EditTool verifies that the edit tool's
// ToolDef declares ReadOnly: false (default, non-read-only).
func TestIntegration_ToolDef_ReadOnly_EditTool(t *testing.T) {
	et := edit.New()
	def := et.ToolDef()

	if def.ReadOnly {
		t.Errorf("edit ToolDef().ReadOnly = true, want false")
	}
}

// TestIntegration_ToolDef_ReadOnly_BashTool verifies that the bash tool's
// ToolDef declares ReadOnly: false (default, non-read-only).
func TestIntegration_ToolDef_ReadOnly_BashTool(t *testing.T) {
	bt := bash.New(bash.WithTimeout(5 * time.Second))
	def := bt.ToolDef()

	if def.ReadOnly {
		t.Errorf("bash ToolDef().ReadOnly = true, want false")
	}
}

// TestIntegration_ToolDef_ReadOnly_RegistryReflectsReadOnly verifies that after
// registering tools in a real registry, the Get method returns the correct
// ReadOnly value for each tool.
func TestIntegration_ToolDef_ReadOnly_RegistryReflectsReadOnly(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyTools := map[string]bool{
		"read": true,
		"glob": true,
		"grep": true,
	}
	writeTools := map[string]bool{
		"write": false,
		"edit":  false,
		"bash":  false,
	}

	for name, wantReadOnly := range readOnlyTools {
		def, ok := reg.Get(name)
		if !ok {
			t.Errorf("tool %q not found in registry", name)
			continue
		}

		if def.ReadOnly != wantReadOnly {
			t.Errorf("registry Get(%q).ReadOnly = %v, want %v", name, def.ReadOnly, wantReadOnly)
		}
	}

	for name, wantReadOnly := range writeTools {
		def, ok := reg.Get(name)
		if !ok {
			t.Errorf("tool %q not found in registry", name)
			continue
		}

		if def.ReadOnly != wantReadOnly {
			t.Errorf("registry Get(%q).ReadOnly = %v, want %v", name, def.ReadOnly, wantReadOnly)
		}
	}
}

// =============================================================================
// Section 6.2: Configuration loading integration tests for permission mode
// =============================================================================

// TestIntegration_Config_PermissionMode_DefaultWhenOmitted verifies that
// when permission_mode is not set in YAML, it defaults to "default".
func TestIntegration_Config_PermissionMode_DefaultWhenOmitted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.CLI.PermissionMode != configs.PermissionModeDefault {
		t.Errorf("PermissionMode = %q, want %q", cfg.CLI.PermissionMode, configs.PermissionModeDefault)
	}
}

// TestIntegration_Config_PermissionMode_LoadedFromYAML verifies that
// permission_mode is correctly loaded from YAML configuration.
func TestIntegration_Config_PermissionMode_LoadedFromYAML(t *testing.T) {
	modes := []configs.PermissionMode{
		configs.PermissionModeDefault,
		configs.PermissionModeAcceptEdits,
		configs.PermissionModeAuto,
		configs.PermissionModePlan,
	}

	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")

			content := fmt.Sprintf(`
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
cli:
  permission_mode: %q
`, mode)
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}

			cfg, err := configs.Load(path, true)
			if err != nil {
				t.Fatalf("configs.Load: %v", err)
			}

			if cfg.CLI.PermissionMode != mode {
				t.Errorf("PermissionMode = %q, want %q", cfg.CLI.PermissionMode, mode)
			}
		})
	}
}

// TestIntegration_Config_PermissionMode_EnvVarOverridesYAML verifies that the
// VV_PERMISSION_MODE environment variable overrides the YAML permission_mode value.
func TestIntegration_Config_PermissionMode_EnvVarOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
cli:
  permission_mode: "default"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_PERMISSION_MODE", "auto")

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.CLI.PermissionMode != configs.PermissionModeAuto {
		t.Errorf("PermissionMode = %q, want %q", cfg.CLI.PermissionMode, configs.PermissionModeAuto)
	}
}

// TestIntegration_Config_PermissionMode_InvalidValueReturnsError verifies that
// an invalid permission_mode value causes Load to return an error.
func TestIntegration_Config_PermissionMode_InvalidValueReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
cli:
  permission_mode: "invalid-mode"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := configs.Load(path, true)
	if err == nil {
		t.Fatal("expected error for invalid permission_mode, got nil")
	}

	if !strings.Contains(err.Error(), "invalid permission_mode") {
		t.Errorf("error = %q, want to contain 'invalid permission_mode'", err.Error())
	}
}

// TestIntegration_Config_PermissionMode_InvalidEnvVarReturnsError verifies that
// an invalid VV_PERMISSION_MODE env var causes Load to return an error.
func TestIntegration_Config_PermissionMode_InvalidEnvVarReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_PERMISSION_MODE", "bogus")

	_, err := configs.Load(path, true)
	if err == nil {
		t.Fatal("expected error for invalid VV_PERMISSION_MODE, got nil")
	}

	if !strings.Contains(err.Error(), "invalid permission_mode") {
		t.Errorf("error = %q, want to contain 'invalid permission_mode'", err.Error())
	}
}

// TestIntegration_Config_PermissionMode_DeprecatedConfirmToolsStillLoads verifies
// that config with deprecated confirm_tools still loads successfully (with the
// deprecation warning logged, which we don't assert on here).
func TestIntegration_Config_PermissionMode_DeprecatedConfirmToolsStillLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
cli:
  confirm_tools:
    - bash
    - write
  permission_mode: "default"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if len(cfg.CLI.ConfirmTools) != 2 {
		t.Errorf("ConfirmTools len = %d, want 2", len(cfg.CLI.ConfirmTools))
	}

	if cfg.CLI.PermissionMode != configs.PermissionModeDefault {
		t.Errorf("PermissionMode = %q, want %q", cfg.CLI.PermissionMode, configs.PermissionModeDefault)
	}
}

// =============================================================================
// Section 6.1: Permission mode decision logic integration tests
// Uses real tool registries (tools.Register) instead of mocks.
// =============================================================================

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

// TestIntegration_Permission_SessionAllowed_BypassesConfirmation verifies that
// a tool marked as session-allowed via "Allow Always" bypasses the confirmation
// dialog on subsequent calls, using the real tool registry.
func TestIntegration_Permission_SessionAllowed_BypassesConfirmation(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModeDefault)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	confirmCount := 0
	ps.SetConfirmFn(func(_ context.Context, _, _ string) (vvcli.PermissionAction, error) {
		confirmCount++
		return vvcli.PermissionAllowAlways, nil
	})

	// First call: triggers confirmation, gets AllowAlways, adds to session.
	result, err := wrapped.Execute(context.Background(), "bash", `{"command":"echo first"}`)
	if err != nil {
		t.Fatalf("Execute(bash) first: %v", err)
	}

	if result.IsError {
		t.Errorf("first call should be approved via AllowAlways, got: %s", resultText(result))
	}

	if confirmCount != 1 {
		t.Errorf("confirmFn called %d times, want 1 after first call", confirmCount)
	}

	// Second call: should bypass confirmation via session-allowed.
	result, err = wrapped.Execute(context.Background(), "bash", `{"command":"echo second"}`)
	if err != nil {
		t.Fatalf("Execute(bash) second: %v", err)
	}

	if result.IsError {
		t.Errorf("second call should be approved via session-allowed, got: %s", resultText(result))
	}

	if confirmCount != 1 {
		t.Errorf("confirmFn called %d times, want 1 (should bypass on second call)", confirmCount)
	}
}

// TestIntegration_Permission_AllowOnce_DoesNotAddToSession verifies that the
// "Allow" (once) action does not add the tool to the session-allowed set, so
// subsequent calls still trigger confirmation.
func TestIntegration_Permission_AllowOnce_DoesNotAddToSession(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModeDefault)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	confirmCount := 0
	ps.SetConfirmFn(func(_ context.Context, _, _ string) (vvcli.PermissionAction, error) {
		confirmCount++
		return vvcli.PermissionAllow, nil
	})

	// First call: triggers confirmation, Allow once.
	_, err = wrapped.Execute(context.Background(), "bash", `{"command":"echo first"}`)
	if err != nil {
		t.Fatalf("Execute(bash) first: %v", err)
	}

	// Second call: should trigger confirmation again (not session-allowed).
	_, err = wrapped.Execute(context.Background(), "bash", `{"command":"echo second"}`)
	if err != nil {
		t.Fatalf("Execute(bash) second: %v", err)
	}

	if confirmCount != 2 {
		t.Errorf("confirmFn called %d times, want 2 (Allow once should not persist)", confirmCount)
	}
}

// TestIntegration_Permission_ModeSwitchClearsSessionAllowed verifies that
// switching permission mode via SetMode clears the session-allowed set, so
// previously allowed tools require confirmation again.
func TestIntegration_Permission_ModeSwitchClearsSessionAllowed(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModeDefault)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	// Mark bash as session-allowed.
	ps.AddSessionAllowed("bash")

	// Verify it is session-allowed.
	if !ps.IsSessionAllowed("bash") {
		t.Fatal("bash should be session-allowed after AddSessionAllowed")
	}

	// Switch mode (should clear session-allowed).
	ps.SetMode(configs.PermissionModeAcceptEdits)

	// Verify session-allowed is cleared.
	if ps.IsSessionAllowed("bash") {
		t.Error("bash should not be session-allowed after mode switch")
	}

	// Bash should now require confirmation in accept-edits mode.
	confirmCalled := false
	ps.SetConfirmFn(func(_ context.Context, _, _ string) (vvcli.PermissionAction, error) {
		confirmCalled = true
		return vvcli.PermissionDeny, nil
	})

	result, err := wrapped.Execute(context.Background(), "bash", `{"command":"echo hello"}`)
	if err != nil {
		t.Fatalf("Execute(bash): %v", err)
	}

	if !confirmCalled {
		t.Error("after mode switch, bash should require confirmation")
	}

	if !result.IsError {
		t.Error("bash should be denied after mode switch to accept-edits")
	}
}

// TestIntegration_Permission_SharedState_AcrossMultipleWrappedRegistries verifies
// that multiple wrapped registries (simulating multiple agents) share the same
// permission state. A tool approved via "Allow Always" on one wrapped registry
// should be session-allowed on all others.
func TestIntegration_Permission_SharedState_AcrossMultipleWrappedRegistries(t *testing.T) {
	reg1, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register (reg1): %v", err)
	}

	reg2, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register (reg2): %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModeDefault)
	wrapped1 := vvcli.WrapRegistryWithPermission(reg1, ps)
	wrapped2 := vvcli.WrapRegistryWithPermission(reg2, ps)

	confirmCount := 0
	ps.SetConfirmFn(func(_ context.Context, _, _ string) (vvcli.PermissionAction, error) {
		confirmCount++
		return vvcli.PermissionAllowAlways, nil
	})

	// Execute bash on wrapped1: triggers confirmation, adds to session.
	result, err := wrapped1.Execute(context.Background(), "bash", `{"command":"echo from reg1"}`)
	if err != nil {
		t.Fatalf("wrapped1.Execute(bash): %v", err)
	}

	if result.IsError {
		t.Errorf("wrapped1: bash should be approved, got: %s", resultText(result))
	}

	if confirmCount != 1 {
		t.Errorf("confirmFn called %d times, want 1 after first call", confirmCount)
	}

	// Execute bash on wrapped2: should bypass confirmation via shared session-allowed.
	result, err = wrapped2.Execute(context.Background(), "bash", `{"command":"echo from reg2"}`)
	if err != nil {
		t.Fatalf("wrapped2.Execute(bash): %v", err)
	}

	if result.IsError {
		t.Errorf("wrapped2: bash should be approved via shared session-allowed, got: %s", resultText(result))
	}

	if confirmCount != 1 {
		t.Errorf("confirmFn called %d times, want 1 (wrapped2 should bypass via shared state)", confirmCount)
	}
}

// TestIntegration_Permission_SetConfirmFn_WiresAllExecutors verifies that
// SetConfirmFn on the shared PermissionState updates the confirmFn on all
// registered executor instances.
func TestIntegration_Permission_SetConfirmFn_WiresAllExecutors(t *testing.T) {
	reg1, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register (reg1): %v", err)
	}

	reg2, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register (reg2): %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModeDefault)
	wrapped1 := vvcli.WrapRegistryWithPermission(reg1, ps)
	wrapped2 := vvcli.WrapRegistryWithPermission(reg2, ps)

	// Default confirmFn allows all. Verify bash is approved before wiring.
	result, err := wrapped1.Execute(context.Background(), "bash", `{"command":"echo hello"}`)
	if err != nil {
		t.Fatalf("wrapped1 pre-wire Execute(bash): %v", err)
	}

	if result.IsError {
		t.Error("pre-wire: bash should be approved by default confirmFn")
	}

	// Now wire a deny confirmFn.
	ps.SetConfirmFn(func(_ context.Context, _, _ string) (vvcli.PermissionAction, error) {
		return vvcli.PermissionDeny, nil
	})

	// Both executors should now deny bash.
	result, err = wrapped1.Execute(context.Background(), "bash", `{"command":"echo hello"}`)
	if err != nil {
		t.Fatalf("wrapped1 post-wire Execute(bash): %v", err)
	}

	if !result.IsError {
		t.Error("post-wire: wrapped1 bash should be denied")
	}

	result, err = wrapped2.Execute(context.Background(), "bash", `{"command":"echo hello"}`)
	if err != nil {
		t.Fatalf("wrapped2 post-wire Execute(bash): %v", err)
	}

	if !result.IsError {
		t.Error("post-wire: wrapped2 bash should be denied")
	}
}

// TestIntegration_Permission_WrappedRegistry_DelegatesListAndGet verifies that
// the wrapped registry correctly delegates List() and Get() to the inner real
// registry, preserving tool count and tool definitions.
func TestIntegration_Permission_WrappedRegistry_DelegatesListAndGet(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModeDefault)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	origList := reg.List()
	wrappedList := wrapped.List()

	if len(wrappedList) != len(origList) {
		t.Errorf("wrapped List() len = %d, original = %d", len(wrappedList), len(origList))
	}

	// Verify each tool is accessible via Get.
	expectedTools := []string{"bash", "read", "write", "edit", "glob", "grep"}
	for _, name := range expectedTools {
		def, ok := wrapped.Get(name)
		if !ok {
			t.Errorf("wrapped Get(%q) not found", name)
			continue
		}

		if def.Name != name {
			t.Errorf("wrapped Get(%q).Name = %q", name, def.Name)
		}
	}
}

// TestIntegration_Permission_ContextCancellation_DuringConfirmation verifies
// that when the context is cancelled while waiting for confirmation, the tool
// call returns an error result.
func TestIntegration_Permission_ContextCancellation_DuringConfirmation(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	ps := vvcli.NewPermissionState(configs.PermissionModeDefault)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	ps.SetConfirmFn(func(ctx context.Context, _, _ string) (vvcli.PermissionAction, error) {
		return vvcli.PermissionDeny, ctx.Err()
	})

	result, err := wrapped.Execute(ctx, "bash", `{"command":"echo hello"}`)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result when context is cancelled during confirmation")
	}
}

// TestIntegration_Permission_UnknownTool_NotReadOnly verifies that a tool not
// found in the registry is treated as non-read-only, so it requires confirmation
// in default mode.
func TestIntegration_Permission_UnknownTool_NotReadOnly(t *testing.T) {
	reg := tool.NewRegistry()
	ps := vvcli.NewPermissionState(configs.PermissionModeDefault)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	confirmCalled := false
	ps.SetConfirmFn(func(_ context.Context, _, _ string) (vvcli.PermissionAction, error) {
		confirmCalled = true
		return vvcli.PermissionDeny, nil
	})

	// Execute a tool that doesn't exist in the registry.
	result, err := wrapped.Execute(context.Background(), "nonexistent_tool", `{}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// It should reach confirmFn since the tool is not found (not read-only).
	if !confirmCalled {
		t.Error("unknown tool should reach confirmFn (not treated as read-only)")
	}

	if !result.IsError {
		t.Error("unknown tool should be denied")
	}
}

// TestIntegration_Permission_PlanMode_UnknownTool_Rejected verifies that in plan
// mode, a tool not found in the registry is rejected (since it's not read-only).
func TestIntegration_Permission_PlanMode_UnknownTool_Rejected(t *testing.T) {
	reg := tool.NewRegistry()
	ps := vvcli.NewPermissionState(configs.PermissionModePlan)
	wrapped := vvcli.WrapRegistryWithPermission(reg, ps)

	result, err := wrapped.Execute(context.Background(), "nonexistent_tool", `{}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !result.IsError {
		t.Error("plan mode: unknown tool should be rejected")
	}

	text := resultText(result)
	if !strings.Contains(text, "not permitted in plan mode") {
		t.Errorf("rejection text = %q, want to contain 'not permitted in plan mode'", text)
	}
}
