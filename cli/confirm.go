package cli

import (
	"context"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
)

// confirmingExecutor wraps a tool.ToolRegistry, intercepting Execute() calls
// for tools that require user confirmation.
type confirmingExecutor struct {
	tool.ToolRegistry
	confirmTools map[string]bool
	confirmFn    func(ctx context.Context, toolName, args string) (bool, error)
}

// newConfirmingExecutor creates a confirmingExecutor wrapping the given registry.
func newConfirmingExecutor(
	inner tool.ToolRegistry,
	confirmTools []string,
	confirmFn func(ctx context.Context, toolName, args string) (bool, error),
) *confirmingExecutor {
	ct := make(map[string]bool, len(confirmTools))
	for _, name := range confirmTools {
		ct[name] = true
	}

	return &confirmingExecutor{
		ToolRegistry: inner,
		confirmTools: ct,
		confirmFn:    confirmFn,
	}
}

// Execute runs the tool, optionally asking for user confirmation first.
func (r *confirmingExecutor) Execute(ctx context.Context, name, args string) (schema.ToolResult, error) {
	if r.confirmTools[name] {
		approved, err := r.confirmFn(ctx, name, args)
		if err != nil {
			return schema.ErrorResult("", err.Error()), nil
		}

		if !approved {
			return schema.ErrorResult("", "Tool call rejected by user"), nil
		}
	}

	return r.ToolRegistry.Execute(ctx, name, args)
}

// WrapRegistry wraps a tool.ToolRegistry with a confirmingExecutor if confirmTools
// is non-empty. The confirmFn will be set later by the App when the TUI starts.
// For now, this returns a placeholder that the App can wire up.
// If confirmTools is empty, returns the original registry unchanged.
func WrapRegistry(inner tool.ToolRegistry, confirmTools []string) tool.ToolRegistry {
	if len(confirmTools) == 0 {
		return inner
	}

	return newConfirmingExecutor(inner, confirmTools, func(_ context.Context, _, _ string) (bool, error) {
		// Default: allow all. The App will replace this function.
		return true, nil
	})
}
