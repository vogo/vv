package dispatches

import (
	"context"
	"testing"
)

func TestDepthFrom_Default(t *testing.T) {
	ctx := context.Background()

	if got := DepthFrom(ctx); got != 0 {
		t.Errorf("DepthFrom(empty ctx) = %d, want 0", got)
	}
}

func TestWithDepth(t *testing.T) {
	ctx := WithDepth(context.Background(), 3)

	if got := DepthFrom(ctx); got != 3 {
		t.Errorf("DepthFrom = %d, want 3", got)
	}
}

func TestIncrementDepth(t *testing.T) {
	ctx := context.Background()

	ctx = IncrementDepth(ctx)
	if got := DepthFrom(ctx); got != 1 {
		t.Errorf("after 1 increment: DepthFrom = %d, want 1", got)
	}

	ctx = IncrementDepth(ctx)
	if got := DepthFrom(ctx); got != 2 {
		t.Errorf("after 2 increments: DepthFrom = %d, want 2", got)
	}
}

func TestWithDepth_Override(t *testing.T) {
	ctx := WithDepth(context.Background(), 5)
	ctx = WithDepth(ctx, 2)

	if got := DepthFrom(ctx); got != 2 {
		t.Errorf("DepthFrom = %d, want 2 (overridden)", got)
	}
}

func TestIncrementDepth_FromExisting(t *testing.T) {
	ctx := WithDepth(context.Background(), 7)
	ctx = IncrementDepth(ctx)

	if got := DepthFrom(ctx); got != 8 {
		t.Errorf("DepthFrom = %d, want 8", got)
	}
}
