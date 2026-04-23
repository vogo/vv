package permission_tests

import (
	"context"
	"strings"
	"testing"

	"github.com/vogo/vage/tool"
	vvcli "github.com/vogo/vv/cli"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/tools"
)

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
