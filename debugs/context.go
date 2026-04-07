package debugs

import "context"

type ctxKey int

const (
	ctxKeyCorrelationID ctxKey = iota
	ctxKeyRequestID
	ctxKeyAgentName
)

// WithCorrelationID returns a new context carrying the correlation id.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyCorrelationID, id)
}

// CorrelationIDFromContext returns the correlation id stored in ctx, if any.
func CorrelationIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyCorrelationID).(string)
	return v
}

// WithRequestID returns a new context carrying the http request id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// RequestIDFromContext returns the http request id stored in ctx, if any.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRequestID).(string)
	return v
}

// WithAgentName returns a new context carrying the dispatching agent name.
func WithAgentName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, ctxKeyAgentName, name)
}

// AgentNameFromContext returns the dispatching agent name stored in ctx, if any.
func AgentNameFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyAgentName).(string)
	return v
}
