package dispatches

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/schema"
)

// extractJSON attempts to extract a JSON object from text that may contain
// markdown code fences or other surrounding text.
func extractJSON(text string) string {
	// Try to find JSON within markdown code fences.
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + len("```json")
		if end := strings.Index(text[start:], "```"); end >= 0 {
			return strings.TrimSpace(text[start : start+end])
		}
	}

	if idx := strings.Index(text, "```"); idx >= 0 {
		start := idx + len("```")
		if end := strings.Index(text[start:], "```"); end >= 0 {
			candidate := strings.TrimSpace(text[start : start+end])
			if strings.HasPrefix(candidate, "{") {
				return candidate
			}
		}
	}

	// Try to find a JSON object directly, skipping braces inside quoted strings.
	if start := strings.Index(text, "{"); start >= 0 {
		depth := 0
		inString := false

		for i := start; i < len(text); i++ {
			ch := text[i]
			if inString {
				if ch == '\\' && i+1 < len(text) {
					i++ // skip escaped character

					continue
				}

				if ch == '"' {
					inString = false
				}

				continue
			}

			switch ch {
			case '"':
				inString = true
			case '{':
				depth++
			case '}':
				depth--

				if depth == 0 {
					return text[start : i+1]
				}
			}
		}
	}

	return text
}

// aggregateUsage merges two usage structs into a single Usage.
func aggregateUsage(a, b *aimodel.Usage) *aimodel.Usage {
	if a == nil && b == nil {
		return nil
	}

	result := &aimodel.Usage{}

	if a != nil {
		result.Add(a)
	}

	if b != nil {
		result.Add(b)
	}

	return result
}

// isEOF checks if an error is io.EOF.
func isEOF(err error) bool {
	return errors.Is(err, io.EOF)
}

// enrichRequest prepends working directory and exploration context to a request for sub-agent dispatch.
func (d *Dispatcher) enrichRequest(req *schema.RunRequest, contextSummary string) *schema.RunRequest {
	if d.workingDir == "" && contextSummary == "" {
		return req
	}

	var msgs []schema.Message

	if d.workingDir != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Working directory: %s", d.workingDir),
		))
	}

	if contextSummary != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Project context:\n%s", contextSummary),
		))
	}

	msgs = append(msgs, req.Messages...)

	return &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
		Options:   req.Options,
		Metadata:  req.Metadata,
	}
}

// fallbackRun delegates to the fallback agent with a warning prepended.
func (d *Dispatcher) fallbackRun(ctx context.Context, req *schema.RunRequest, classifyUsage *aimodel.Usage) (*schema.RunResponse, error) {
	if d.fallbackAgent == nil {
		return nil, fmt.Errorf("orchestrator: no fallback agent available")
	}

	msgs := make([]schema.Message, 0, len(req.Messages)+1)
	msgs = append(msgs, schema.NewUserMessage("Note: task classification failed, executing as a general conversation."))
	msgs = append(msgs, req.Messages...)

	fallbackReq := &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
		Options:   req.Options,
		Metadata:  req.Metadata,
	}

	resp, err := d.fallbackAgent.Run(ctx, fallbackReq)
	if err != nil {
		return nil, err
	}

	resp.Usage = aggregateUsage(classifyUsage, resp.Usage)

	return resp, nil
}
