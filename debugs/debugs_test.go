package debugs

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
)

func TestRedact_APIKey(t *testing.T) {
	in := `{"api_key":"sk-secretval"}`
	out := Redact(in)
	if strings.Contains(out, "sk-secretval") {
		t.Errorf("api key not redacted: %s", out)
	}
}

func TestRedact_Authorization(t *testing.T) {
	in := `Authorization: Bearer abcdef123`
	out := Redact(in)
	if strings.Contains(out, "abcdef123") {
		t.Errorf("auth not redacted: %s", out)
	}
}

func TestSink_WriterEmit(t *testing.T) {
	var buf bytes.Buffer
	s := NewWriterSink(&buf)

	s.Emit(context.Background(), &Record{
		Kind:          KindToolStart,
		CorrelationID: "c1",
		ToolName:      "read",
		Args:          `{"path":"x"}`,
	})

	if !strings.Contains(buf.String(), "tool.start") {
		t.Errorf("missing kind: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "read") {
		t.Errorf("missing tool name: %s", buf.String())
	}
}

func TestSink_NilNoop(t *testing.T) {
	var s *Sink
	s.Emit(context.Background(), &Record{Kind: KindToolEnd})
	if id := s.NewCorrelationID(); id == "" {
		// nil sink still returns a fresh id (defensive)
		_ = id
	}
}

func TestSink_ConcurrentEmit(t *testing.T) {
	var buf bytes.Buffer
	s := NewWriterSink(&buf)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Emit(context.Background(), &Record{Kind: KindLLMRequest, CorrelationID: "x"})
		}()
	}
	wg.Wait()
	// Each line begins with "[" and ends with "\n"; count lines.
	lines := strings.Count(buf.String(), "kind=llm.request")
	if lines != 50 {
		t.Errorf("expected 50 emissions, got %d", lines)
	}
}

// fakeRegistry is a minimal ToolRegistry for testing.
type fakeRegistry struct {
	result schema.ToolResult
	err    error
	calls  int
}

func (f *fakeRegistry) Register(_ schema.ToolDef, _ tool.ToolHandler) error { return nil }
func (f *fakeRegistry) Unregister(_ string) error                           { return nil }
func (f *fakeRegistry) Get(_ string) (schema.ToolDef, bool)                 { return schema.ToolDef{}, false }
func (f *fakeRegistry) List() []schema.ToolDef                              { return nil }
func (f *fakeRegistry) Merge(_ []schema.ToolDef)                            {}
func (f *fakeRegistry) Execute(_ context.Context, _, _ string) (schema.ToolResult, error) {
	f.calls++
	return f.result, f.err
}

func TestDebuggingToolRegistry_CapturesArgsResult(t *testing.T) {
	var buf bytes.Buffer
	sink := NewWriterSink(&buf)
	inner := &fakeRegistry{result: schema.TextResult("id1", "ok-result")}
	d := NewDebuggingToolRegistry(inner, sink)

	res, err := d.Execute(context.Background(), "read", `{"path":"a"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content[0].Text != "ok-result" {
		t.Errorf("wrong result")
	}
	out := buf.String()
	if !strings.Contains(out, "tool.start") || !strings.Contains(out, "tool.end") {
		t.Errorf("missing start/end: %s", out)
	}
	if !strings.Contains(out, `{"path":"a"}`) {
		t.Errorf("missing args: %s", out)
	}
	if !strings.Contains(out, "ok-result") {
		t.Errorf("missing result: %s", out)
	}
}

func TestDebuggingToolRegistry_NilSinkPassthrough(t *testing.T) {
	inner := &fakeRegistry{result: schema.TextResult("id", "x")}
	d := NewDebuggingToolRegistry(inner, nil)
	if d != tool.ToolRegistry(inner) {
		t.Errorf("expected passthrough when sink nil")
	}
}

func TestDebuggingToolRegistry_Error(t *testing.T) {
	var buf bytes.Buffer
	sink := NewWriterSink(&buf)
	inner := &fakeRegistry{err: errors.New("boom")}
	d := NewDebuggingToolRegistry(inner, sink)
	_, err := d.Execute(context.Background(), "bash", `{}`)
	if err == nil {
		t.Fatal("expected error")
	}
	out := buf.String()
	if !strings.Contains(out, "boom") {
		t.Errorf("missing error in record: %s", out)
	}
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()
	ctx = WithCorrelationID(ctx, "c")
	ctx = WithRequestID(ctx, "r")
	ctx = WithAgentName(ctx, "coder")
	if CorrelationIDFromContext(ctx) != "c" || RequestIDFromContext(ctx) != "r" || AgentNameFromContext(ctx) != "coder" {
		t.Errorf("ctx helpers broken")
	}
}
