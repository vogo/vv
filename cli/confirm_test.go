package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
)

// mockRegistry is a minimal ToolRegistry for testing.
type mockRegistry struct {
	executeFn func(ctx context.Context, name, args string) (schema.ToolResult, error)
	tools     map[string]schema.ToolDef
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{
		tools: make(map[string]schema.ToolDef),
		executeFn: func(_ context.Context, name, _ string) (schema.ToolResult, error) {
			return schema.TextResult("", "executed: "+name), nil
		},
	}
}

func (m *mockRegistry) Execute(ctx context.Context, name, args string) (schema.ToolResult, error) {
	return m.executeFn(ctx, name, args)
}

func (m *mockRegistry) Register(def schema.ToolDef, _ tool.ToolHandler) error {
	m.tools[def.Name] = def
	return nil
}

func (m *mockRegistry) Unregister(name string) error {
	delete(m.tools, name)
	return nil
}

func (m *mockRegistry) Get(name string) (schema.ToolDef, bool) {
	d, ok := m.tools[name]
	return d, ok
}

func (m *mockRegistry) List() []schema.ToolDef {
	defs := make([]schema.ToolDef, 0, len(m.tools))
	for _, d := range m.tools {
		defs = append(defs, d)
	}
	return defs
}

func (m *mockRegistry) Merge(defs []schema.ToolDef) {
	for _, d := range defs {
		m.tools[d.Name] = d
	}
}

var _ tool.ToolRegistry = (*mockRegistry)(nil)

func TestConfirmingExecutorApprove(t *testing.T) {
	inner := newMockRegistry()
	executor := newConfirmingExecutor(inner, []string{"bash"}, func(_ context.Context, _, _ string) (bool, error) {
		return true, nil
	})

	result, err := executor.Execute(context.Background(), "bash", `{"command":"ls"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.IsError {
		t.Error("expected non-error result when approved")
	}

	text := ""
	for _, p := range result.Content {
		if p.Type == "text" {
			text = p.Text
			break
		}
	}

	if text != "executed: bash" {
		t.Errorf("result text = %q, want %q", text, "executed: bash")
	}
}

func TestConfirmingExecutorReject(t *testing.T) {
	inner := newMockRegistry()
	executor := newConfirmingExecutor(inner, []string{"bash"}, func(_ context.Context, _, _ string) (bool, error) {
		return false, nil
	})

	result, err := executor.Execute(context.Background(), "bash", `{"command":"rm -rf /"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result when rejected")
	}

	text := ""
	for _, p := range result.Content {
		if p.Type == "text" {
			text = p.Text
			break
		}
	}

	if text != "Tool call rejected by user" {
		t.Errorf("result text = %q, want %q", text, "Tool call rejected by user")
	}
}

func TestConfirmingExecutorPassthrough(t *testing.T) {
	inner := newMockRegistry()
	confirmCalled := false
	executor := newConfirmingExecutor(inner, []string{"bash"}, func(_ context.Context, _, _ string) (bool, error) {
		confirmCalled = true
		return true, nil
	})

	// "read" is not in the confirm list, so it should pass through.
	result, err := executor.Execute(context.Background(), "read", `{"path":"file.go"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if confirmCalled {
		t.Error("confirmFn should not be called for non-confirmed tool")
	}

	if result.IsError {
		t.Error("expected non-error result for passthrough")
	}
}

func TestConfirmingExecutorCancelledCtx(t *testing.T) {
	inner := newMockRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	executor := newConfirmingExecutor(inner, []string{"bash"}, func(ctx context.Context, _, _ string) (bool, error) {
		return false, ctx.Err()
	})

	result, err := executor.Execute(ctx, "bash", `{"command":"ls"}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	// Should return an error result (not a Go error) with the context error message.
	if !result.IsError {
		t.Error("expected error result when context is cancelled")
	}
}

func TestConfirmingExecutorConfirmFnError(t *testing.T) {
	inner := newMockRegistry()
	executor := newConfirmingExecutor(inner, []string{"bash"}, func(_ context.Context, _, _ string) (bool, error) {
		return false, errors.New("network error")
	})

	result, err := executor.Execute(context.Background(), "bash", `{"command":"ls"}`)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result when confirmFn returns error")
	}

	text := ""
	for _, p := range result.Content {
		if p.Type == "text" {
			text = p.Text
			break
		}
	}

	if text != "network error" {
		t.Errorf("result text = %q, want %q", text, "network error")
	}
}

func TestConfirmingExecutorDelegation(t *testing.T) {
	inner := newMockRegistry()

	executor := newConfirmingExecutor(inner, []string{"bash"}, func(_ context.Context, _, _ string) (bool, error) {
		return true, nil
	})

	// Test Register delegation.
	def := schema.ToolDef{Name: "test_tool", Description: "A test tool"}
	if err := executor.Register(def, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Test Get delegation.
	got, ok := executor.Get("test_tool")
	if !ok {
		t.Fatal("Get: tool not found")
	}

	if got.Name != "test_tool" {
		t.Errorf("Get name = %q, want %q", got.Name, "test_tool")
	}

	// Test List delegation.
	list := executor.List()
	if len(list) != 1 {
		t.Errorf("List len = %d, want 1", len(list))
	}

	// Test Merge delegation.
	executor.Merge([]schema.ToolDef{{Name: "merged_tool", Description: "merged"}})
	if _, ok := executor.Get("merged_tool"); !ok {
		t.Error("Merge: merged tool not found")
	}

	// Test Unregister delegation.
	if err := executor.Unregister("test_tool"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	if _, ok := executor.Get("test_tool"); ok {
		t.Error("Unregister: tool should be removed")
	}
}

func TestWrapRegistryNoConfirmTools(t *testing.T) {
	inner := newMockRegistry()
	result := WrapRegistry(inner, nil)

	// Should return the inner registry unchanged.
	if result != inner {
		t.Error("WrapRegistry with no confirm tools should return inner registry")
	}
}

func TestWrapRegistryWithConfirmTools(t *testing.T) {
	inner := newMockRegistry()
	result := WrapRegistry(inner, []string{"bash"})

	// Should return a confirmingExecutor.
	if _, ok := result.(*confirmingExecutor); !ok {
		t.Errorf("WrapRegistry should return *confirmingExecutor, got %T", result)
	}

	// Default confirmFn should allow all.
	tr, err := result.Execute(context.Background(), "bash", `{"command":"ls"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if tr.IsError {
		t.Error("default confirmFn should allow tool execution")
	}
}
