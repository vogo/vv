package lifecycle

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/vogo/vage/schema"
)

// Hook defines lifecycle callbacks for agent executions.
type Hook interface {
	// OnBeforeRun is called before an agent runs. Returning an error aborts the run.
	OnBeforeRun(ctx context.Context, agentID string, req *schema.RunRequest) error
	// OnAfterRun is called after an agent runs. Errors are logged but do not fail the response.
	OnAfterRun(ctx context.Context, agentID string, resp *schema.RunResponse, err error)
}

// Chain composes multiple hooks into a single Hook.
// OnBeforeRun calls are executed in order; if any returns an error, subsequent hooks are skipped.
// OnAfterRun calls are executed in reverse order (stack-like unwinding).
func Chain(hooks ...Hook) Hook {
	if len(hooks) == 0 {
		return &noopHook{}
	}

	if len(hooks) == 1 {
		return hooks[0]
	}

	return &chainedHook{hooks: hooks}
}

// chainedHook implements Hook by invoking a slice of hooks.
type chainedHook struct {
	hooks []Hook
}

func (c *chainedHook) OnBeforeRun(ctx context.Context, agentID string, req *schema.RunRequest) error {
	for _, h := range c.hooks {
		if err := h.OnBeforeRun(ctx, agentID, req); err != nil {
			return err
		}
	}

	return nil
}

func (c *chainedHook) OnAfterRun(ctx context.Context, agentID string, resp *schema.RunResponse, err error) {
	for i := len(c.hooks) - 1; i >= 0; i-- {
		c.hooks[i].OnAfterRun(ctx, agentID, resp, err)
	}
}

// noopHook is a no-operation hook.
type noopHook struct{}

func (n *noopHook) OnBeforeRun(_ context.Context, _ string, _ *schema.RunRequest) error { return nil }
func (n *noopHook) OnAfterRun(_ context.Context, _ string, _ *schema.RunResponse, _ error) {
}

// LoggingHook logs agent start/end events.
type LoggingHook struct {
	Logger *slog.Logger
}

func (h *LoggingHook) OnBeforeRun(_ context.Context, agentID string, _ *schema.RunRequest) error {
	h.Logger.Info("agent starting", "agent_id", agentID)

	return nil
}

func (h *LoggingHook) OnAfterRun(_ context.Context, agentID string, _ *schema.RunResponse, err error) {
	if err != nil {
		h.Logger.Warn("agent completed with error", "agent_id", agentID, "error", err)
	} else {
		h.Logger.Info("agent completed", "agent_id", agentID)
	}
}

// MetricsHook is a stub for future metrics collection.
type MetricsHook struct{}

func (h *MetricsHook) OnBeforeRun(_ context.Context, _ string, _ *schema.RunRequest) error {
	return nil
}

func (h *MetricsHook) OnAfterRun(_ context.Context, _ string, _ *schema.RunResponse, _ error) {}

// RateLimitHook is a stub for future rate limiting.
type RateLimitHook struct{}

func (h *RateLimitHook) OnBeforeRun(_ context.Context, _ string, _ *schema.RunRequest) error {
	return nil
}

func (h *RateLimitHook) OnAfterRun(_ context.Context, _ string, _ *schema.RunResponse, _ error) {}

// TimingHook is a convenience hook that records execution time via the logger.
// It is safe for concurrent use.
type TimingHook struct {
	Logger *slog.Logger
	mu     sync.Mutex
	starts map[string]time.Time
}

func (h *TimingHook) OnBeforeRun(_ context.Context, agentID string, _ *schema.RunRequest) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.starts == nil {
		h.starts = make(map[string]time.Time)
	}

	h.starts[agentID] = time.Now()

	return nil
}

func (h *TimingHook) OnAfterRun(_ context.Context, agentID string, _ *schema.RunResponse, _ error) {
	h.mu.Lock()
	start, ok := h.starts[agentID]
	if ok {
		delete(h.starts, agentID)
	}
	h.mu.Unlock()

	if ok {
		h.Logger.Info("agent execution time", "agent_id", agentID, "duration_ms", time.Since(start).Milliseconds())
	}
}
