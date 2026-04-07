// Package debugs provides debug record capture and routing for vv. It defines
// a mode-aware Sink that routes LLM and tool I/O records to a file (TUI),
// stderr (-p mode), or slog (HTTP) and a tool registry decorator that captures
// tool execution. Default-off; the sink is only constructed when --debug is
// enabled.
package debugs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/vogo/vage/largemodel"
)

// Kind identifies the type of debug record.
type Kind string

const (
	KindLLMRequest  Kind = "llm.request"
	KindLLMResponse Kind = "llm.response"
	KindLLMError    Kind = "llm.error"
	KindToolStart   Kind = "tool.start"
	KindToolEnd     Kind = "tool.end"
)

// Record holds a single debug event.
type Record struct {
	Kind          Kind
	CorrelationID string
	HTTPRequestID string
	AgentName     string
	Timestamp     time.Time
	Duration      time.Duration

	// Generic fields populated from the source map; preserved for formatters.
	Fields map[string]any

	// Tool-specific
	ToolName   string
	ToolSource string
	Args       string
	Result     string
	ReadOnly   bool

	Err string
}

// Sink writes debug records via a pluggable backend. Sink is safe for
// concurrent use; backend writes are mutex-guarded so records do not interleave.
type Sink struct {
	mu      sync.Mutex
	backend backend
	closer  io.Closer
}

type backend interface {
	write(r *Record)
}

// Emit forwards a record to the backend.
func (s *Sink) Emit(_ context.Context, r *Record) {
	if s == nil || s.backend == nil || r == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.backend.write(r)
}

// Close releases any underlying resources held by the sink.
func (s *Sink) Close() error {
	if s == nil || s.closer == nil {
		return nil
	}
	return s.closer.Close()
}

// NewCorrelationID returns a fresh correlation id.
func (s *Sink) NewCorrelationID() string { return newID() }

// NewWriterSink creates a sink that writes plain-text records to w.
func NewWriterSink(w io.Writer) *Sink {
	return &Sink{backend: &writerBackend{w: w}}
}

// NewFileSink creates a sink that writes records to a file at path.
// The parent directory is created if needed.
func NewFileSink(path string) (*Sink, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create debug dir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open debug file: %w", err)
	}

	return &Sink{backend: &writerBackend{w: f}, closer: f}, nil
}

// NewSlogSink creates a sink that writes records via slog.
func NewSlogSink(logger *slog.Logger) *Sink {
	if logger == nil {
		logger = slog.Default()
	}
	return &Sink{backend: &slogBackend{logger: logger}}
}

// DefaultFilePath returns the default debug log path for the current process.
// VV_DEBUG_FILE overrides; otherwise ~/.vv/debug-<pid>.log is used.
func DefaultFilePath() string {
	if v := os.Getenv("VV_DEBUG_FILE"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	dir := ".vv"
	if err == nil {
		dir = filepath.Join(home, ".vv")
	}
	return filepath.Join(dir, "debug-"+strconv.Itoa(os.Getpid())+".log")
}

// SinkAdapter wraps a *Sink so it satisfies largemodel.DebugSink. It
// translates the flat fields map produced by the middleware into a typed
// Record before forwarding to the underlying Sink.
type SinkAdapter struct{ S *Sink }

// Emit implements largemodel.DebugSink.
func (a SinkAdapter) Emit(ctx context.Context, kind, corr string, fields map[string]any) {
	if a.S == nil {
		return
	}

	r := &Record{
		Kind:          Kind(kind),
		CorrelationID: corr,
		HTTPRequestID: RequestIDFromContext(ctx),
		AgentName:     AgentNameFromContext(ctx),
		Timestamp:     time.Now(),
		Fields:        fields,
	}

	if d, ok := fields["duration"].(time.Duration); ok {
		r.Duration = d
	}
	if e, ok := fields["error"].(string); ok {
		r.Err = e
	}

	a.S.Emit(ctx, r)
}

// NewCorrelationID implements largemodel.DebugSink.
func (a SinkAdapter) NewCorrelationID() string {
	if a.S == nil {
		return newID()
	}
	return a.S.NewCorrelationID()
}

// Compile-time assertion: SinkAdapter satisfies largemodel.DebugSink.
var _ largemodel.DebugSink = SinkAdapter{}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:])
}
