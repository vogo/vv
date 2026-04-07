package debugs

import (
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
)

type writerBackend struct {
	w io.Writer
}

func (b *writerBackend) write(r *Record) {
	_, _ = io.WriteString(b.w, formatRecord(r))
}

type slogBackend struct {
	logger *slog.Logger
}

func (b *slogBackend) write(r *Record) {
	attrs := []any{
		"correlation_id", r.CorrelationID,
	}
	if r.HTTPRequestID != "" {
		attrs = append(attrs, "http_request_id", r.HTTPRequestID)
	}
	if r.AgentName != "" {
		attrs = append(attrs, "agent", r.AgentName)
	}
	if r.Duration > 0 {
		attrs = append(attrs, "duration_ms", r.Duration.Milliseconds())
	}
	if r.ToolName != "" {
		attrs = append(attrs, "tool", r.ToolName)
	}
	if r.Err != "" {
		attrs = append(attrs, "error", r.Err)
	}
	for k, v := range r.Fields {
		attrs = append(attrs, k, v)
	}
	b.logger.Info(string(r.Kind), attrs...)
}

// formatRecord renders a record as a multi-line plain-text block.
func formatRecord(r *Record) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s] kind=%s corr=%s", r.Timestamp.Format("15:04:05.000"), r.Kind, r.CorrelationID)
	if r.AgentName != "" {
		fmt.Fprintf(&sb, " agent=%s", r.AgentName)
	}
	if r.HTTPRequestID != "" {
		fmt.Fprintf(&sb, " req=%s", r.HTTPRequestID)
	}
	if r.Duration > 0 {
		fmt.Fprintf(&sb, " dur=%s", r.Duration)
	}
	if r.ToolName != "" {
		fmt.Fprintf(&sb, " tool=%s", r.ToolName)
	}
	if r.Err != "" {
		fmt.Fprintf(&sb, " err=%q", r.Err)
	}
	sb.WriteByte('\n')

	if r.Args != "" {
		fmt.Fprintf(&sb, "  args: %s\n", truncateLine(r.Args, 4096))
	}
	if r.Result != "" {
		fmt.Fprintf(&sb, "  result: %s\n", truncateLine(r.Result, 4096))
	}

	if len(r.Fields) > 0 {
		keys := make([]string, 0, len(r.Fields))
		for k := range r.Fields {
			if k == "duration" || k == "error" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&sb, "  %s: %v\n", k, r.Fields[k])
		}
	}

	sb.WriteString("\n--------\n")

	return sb.String()
}

func truncateLine(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
