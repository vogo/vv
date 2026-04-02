package dispatches

import "context"

type depthKey struct{}

// WithDepth returns a context with the given recursion depth.
func WithDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, depthKey{}, depth)
}

// DepthFrom returns the recursion depth from the context (0 if not set).
func DepthFrom(ctx context.Context) int {
	if v, ok := ctx.Value(depthKey{}).(int); ok {
		return v
	}

	return 0
}

// IncrementDepth returns a new context with depth incremented by 1.
func IncrementDepth(ctx context.Context) context.Context {
	return WithDepth(ctx, DepthFrom(ctx)+1)
}
