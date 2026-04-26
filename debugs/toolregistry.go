package debugs

import (
	"context"
	"strings"
	"time"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
)

// DebuggingToolRegistry decorates a tool.ToolRegistry, emitting tool.start
// and tool.end records for each Execute call. All other methods delegate
// unchanged.
type DebuggingToolRegistry struct {
	inner tool.ToolRegistry
	sink  *Sink
}

// Compile-time check.
var _ tool.ToolRegistry = (*DebuggingToolRegistry)(nil)

// NewDebuggingToolRegistry wraps inner with a debug decorator. If sink is
// nil, the inner registry is returned unchanged.
func NewDebuggingToolRegistry(inner tool.ToolRegistry, sink *Sink) tool.ToolRegistry {
	if sink == nil {
		return inner
	}
	return &DebuggingToolRegistry{inner: inner, sink: sink}
}

// Register delegates.
func (d *DebuggingToolRegistry) Register(def schema.ToolDef, h tool.ToolHandler) error {
	return d.inner.Register(def, h)
}

// Unregister delegates.
func (d *DebuggingToolRegistry) Unregister(name string) error {
	return d.inner.Unregister(name)
}

// Get delegates.
func (d *DebuggingToolRegistry) Get(name string) (schema.ToolDef, bool) {
	return d.inner.Get(name)
}

// List delegates.
func (d *DebuggingToolRegistry) List() []schema.ToolDef { return d.inner.List() }

// Merge delegates.
func (d *DebuggingToolRegistry) Merge(defs []schema.ToolDef) { d.inner.Merge(defs) }

// Execute captures tool input/output and forwards to the sink.
func (d *DebuggingToolRegistry) Execute(ctx context.Context, name, args string) (schema.ToolResult, error) {
	corr := d.sink.NewCorrelationID()
	start := time.Now()

	source := classifySource(name)
	d.sink.Emit(ctx, &Record{
		Kind:          KindToolStart,
		CorrelationID: corr,
		HTTPRequestID: RequestIDFromContext(ctx),
		AgentName:     AgentNameFromContext(ctx),
		Timestamp:     start,
		ToolName:      name,
		ToolSource:    source,
		Args:          args,
		ReadOnly:      isReadOnly(name),
	})

	result, err := d.inner.Execute(ctx, name, args)
	dur := time.Since(start)

	end := &Record{
		Kind:          KindToolEnd,
		CorrelationID: corr,
		HTTPRequestID: RequestIDFromContext(ctx),
		AgentName:     AgentNameFromContext(ctx),
		Timestamp:     time.Now(),
		Duration:      dur,
		ToolName:      name,
		ToolSource:    source,
		ReadOnly:      isReadOnly(name),
		Result:        toolResultText(result),
	}

	if err != nil {
		end.Err = err.Error()
	}

	d.sink.Emit(ctx, end)

	return result, err
}

func toolResultText(r schema.ToolResult) string {
	var sb strings.Builder
	for _, p := range r.Content {
		if p.Type == "text" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

var (
	builtinTools  = map[string]bool{"bash": true, "read": true, "web_fetch": true, "web_search": true, "write": true, "edit": true, "glob": true, "grep": true}
	readOnlyTools = map[string]bool{"read": true, "web_fetch": true, "web_search": true, "glob": true, "grep": true}
)

func classifySource(name string) string {
	if builtinTools[name] {
		return "builtin"
	}
	if name == "ask_user" {
		return "builtin"
	}
	// Heuristic: agent-as-tool tends to be lowercase agent ids; MCP tools often
	// have provider prefixes. Default to "mcp".
	return "mcp"
}

func isReadOnly(name string) bool {
	return readOnlyTools[name]
}
