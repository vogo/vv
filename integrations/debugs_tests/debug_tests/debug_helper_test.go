package debug_tests

import (
	"context"
	"errors"
	"sync"

	largemodel "github.com/vogo/largemodel/model"
	"github.com/vogo/largemodel/model/middleware"
	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/debugs"
)

// --------------------------------------------------------------------------
// Fakes
// --------------------------------------------------------------------------

// fakeCompleter is a deterministic ChatCompleter that returns a canned reply.
type fakeCompleter struct {
	resp *largemodel.Response
	err  error
}

func (f *fakeCompleter) Protocol() schema.Protocol { return schema.ProtocolOpenAIChat }

func (f *fakeCompleter) Call(_ context.Context, _ *largemodel.Request) (*largemodel.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func (f *fakeCompleter) CallStream(_ context.Context, _ *largemodel.Request) (*largemodel.Stream, error) {
	return nil, errors.New("stream not implemented in fake")
}

// fakeToolRegistry is a minimal tool.ToolRegistry returning canned results.
type fakeToolRegistry struct {
	mu      sync.Mutex
	calls   []string
	result  schema.ToolResult
	execErr error
}

func (f *fakeToolRegistry) Register(_ schema.ToolDef, _ tool.ToolHandler) error { return nil }
func (f *fakeToolRegistry) Unregister(_ string) error                           { return nil }
func (f *fakeToolRegistry) Get(_ string) (schema.ToolDef, bool)                 { return schema.ToolDef{}, false }
func (f *fakeToolRegistry) List() []schema.ToolDef                              { return nil }
func (f *fakeToolRegistry) Merge(_ []schema.ToolDef)                            {}
func (f *fakeToolRegistry) Execute(_ context.Context, name, args string) (schema.ToolResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, name+":"+args)
	f.mu.Unlock()
	if f.execErr != nil {
		return schema.ToolResult{}, f.execErr
	}
	return f.result, nil
}

// helper: build a debug-wrapped completer the same way setup.Init does.
func wrapWithDebug(base largemodel.Caller, sink *debugs.Sink) largemodel.Caller {
	return largemodel.Chain(base, middleware.NewDebugMiddleware(debugs.SinkAdapter{S: sink}))
}
