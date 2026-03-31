package lifecycle

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/vogo/vage/schema"
)

// recordingHook records hook calls for verification.
type recordingHook struct {
	beforeIDs []string
	afterIDs  []string
	beforeErr error
}

func (r *recordingHook) OnBeforeRun(_ context.Context, agentID string, _ *schema.RunRequest) error {
	r.beforeIDs = append(r.beforeIDs, agentID)

	return r.beforeErr
}

func (r *recordingHook) OnAfterRun(_ context.Context, agentID string, _ *schema.RunResponse, _ error) {
	r.afterIDs = append(r.afterIDs, agentID)
}

func TestChain_BeforeRun_Order(t *testing.T) {
	h1 := &recordingHook{}
	h2 := &recordingHook{}

	chain := Chain(h1, h2)

	err := chain.OnBeforeRun(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("OnBeforeRun: %v", err)
	}

	if len(h1.beforeIDs) != 1 || h1.beforeIDs[0] != "test" {
		t.Errorf("h1 before = %v, want [test]", h1.beforeIDs)
	}

	if len(h2.beforeIDs) != 1 || h2.beforeIDs[0] != "test" {
		t.Errorf("h2 before = %v, want [test]", h2.beforeIDs)
	}
}

func TestChain_BeforeRun_Abort(t *testing.T) {
	h1 := &recordingHook{beforeErr: errors.New("abort")}
	h2 := &recordingHook{}

	chain := Chain(h1, h2)

	err := chain.OnBeforeRun(context.Background(), "test", nil)
	if err == nil {
		t.Fatal("expected error")
	}

	// h2 should not have been called.
	if len(h2.beforeIDs) != 0 {
		t.Errorf("h2 should not be called after abort, got %v", h2.beforeIDs)
	}
}

func TestChain_AfterRun_ReverseOrder(t *testing.T) {
	var order []string

	h1 := &recordingHook{}
	h2 := &recordingHook{}

	// Use a wrapper to track order.
	type orderHook struct {
		Hook
		name string
	}

	oh1 := &orderHook{Hook: h1, name: "h1"}
	oh2 := &orderHook{Hook: h2, name: "h2"}

	_ = oh1
	_ = oh2

	// Manual test for reverse order.
	chain := Chain(h1, h2)
	chain.OnAfterRun(context.Background(), "test", nil, nil)

	// Both should be called.
	if len(h1.afterIDs) != 1 {
		t.Errorf("h1 after = %v", h1.afterIDs)
	}

	if len(h2.afterIDs) != 1 {
		t.Errorf("h2 after = %v", h2.afterIDs)
	}

	// Verify reverse order by using hooks with side effects.
	order = nil

	hook1 := &sideEffectHook{name: "first", order: &order}
	hook2 := &sideEffectHook{name: "second", order: &order}
	chain2 := Chain(hook1, hook2)
	chain2.OnAfterRun(context.Background(), "test", nil, nil)

	if len(order) != 2 || order[0] != "second" || order[1] != "first" {
		t.Errorf("after order = %v, want [second, first]", order)
	}
}

type sideEffectHook struct {
	name  string
	order *[]string
}

func (h *sideEffectHook) OnBeforeRun(_ context.Context, _ string, _ *schema.RunRequest) error {
	return nil
}

func (h *sideEffectHook) OnAfterRun(_ context.Context, _ string, _ *schema.RunResponse, _ error) {
	*h.order = append(*h.order, h.name)
}

func TestChain_Empty(t *testing.T) {
	chain := Chain()

	err := chain.OnBeforeRun(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("OnBeforeRun: %v", err)
	}

	// Should not panic.
	chain.OnAfterRun(context.Background(), "test", nil, nil)
}

func TestChain_Single(t *testing.T) {
	h := &recordingHook{}
	chain := Chain(h)

	_ = chain.OnBeforeRun(context.Background(), "test", nil)

	if len(h.beforeIDs) != 1 {
		t.Errorf("before = %v, want [test]", h.beforeIDs)
	}
}

func TestLoggingHook(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	hook := &LoggingHook{Logger: logger}

	// Should not panic.
	err := hook.OnBeforeRun(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("OnBeforeRun: %v", err)
	}

	hook.OnAfterRun(context.Background(), "test", nil, nil)
	hook.OnAfterRun(context.Background(), "test", nil, errors.New("test error"))
}

func TestMetricsHook(t *testing.T) {
	hook := &MetricsHook{}

	err := hook.OnBeforeRun(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("OnBeforeRun: %v", err)
	}

	hook.OnAfterRun(context.Background(), "test", nil, nil)
}

func TestRateLimitHook(t *testing.T) {
	hook := &RateLimitHook{}

	err := hook.OnBeforeRun(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("OnBeforeRun: %v", err)
	}

	hook.OnAfterRun(context.Background(), "test", nil, nil)
}
