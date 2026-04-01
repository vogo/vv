package dispatches

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

// explore calls the explorer sub-agent to build project context.
// Returns the context summary text and usage. If no explorer is configured
// or exploration fails, returns empty summary.
func (d *Dispatcher) explore(ctx context.Context, req *schema.RunRequest) (string, *aimodel.Usage) {
	if d.explorerAgent == nil {
		return "", nil
	}

	// Build explorer request with working directory context.
	var msgs []schema.Message

	if d.workingDir != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Working directory: %s", d.workingDir),
		))
	}

	msgs = append(msgs, req.Messages...)

	explorerReq := &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
	}

	resp, err := d.explorerAgent.Run(ctx, explorerReq)
	if err != nil {
		slog.Warn("orchestrator: explorer failed", "error", err)

		return "", nil
	}

	if len(resp.Messages) == 0 {
		return "", resp.Usage
	}

	return resp.Messages[0].Content.Text(), resp.Usage
}

// exploreStream calls the explorer sub-agent using streaming, forwarding tool events via send.
// Returns the context summary text and usage. If no explorer is configured
// or exploration fails, returns empty summary.
func (d *Dispatcher) exploreStream(
	ctx context.Context,
	req *schema.RunRequest,
	send func(schema.Event) error,
) (string, *aimodel.Usage) {
	if d.explorerAgent == nil {
		return "", nil
	}

	// Try streaming path.
	sa, ok := d.explorerAgent.(agent.StreamAgent)
	if !ok {
		// Fallback to non-streaming.
		return d.explore(ctx, req)
	}

	// Build explorer request with working directory context (same as explore).
	var msgs []schema.Message

	if d.workingDir != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Working directory: %s", d.workingDir),
		))
	}

	msgs = append(msgs, req.Messages...)

	explorerReq := &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
	}

	stream, err := sa.RunStream(ctx, explorerReq)
	if err != nil {
		slog.Warn("orchestrator: explorer stream failed", "error", err)

		return "", nil
	}

	defer func() { _ = stream.Close() }()

	var textBuf strings.Builder

	var usage aimodel.Usage

	var hasUsage bool

	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}

			slog.Warn("orchestrator: explorer stream recv error", "error", recvErr)

			break
		}

		switch event.Type {
		case schema.EventTextDelta:
			if data, ok := event.Data.(schema.TextDeltaData); ok {
				textBuf.WriteString(data.Delta)
			}
		case schema.EventToolCallStart, schema.EventToolResult, schema.EventError:
			if err := send(event); err != nil {
				slog.Warn("orchestrator: explorer stream send error", "error", err)
			}
		case schema.EventLLMCallEnd:
			if data, ok := event.Data.(schema.LLMCallEndData); ok {
				hasUsage = true
				usage.PromptTokens += data.PromptTokens
				usage.CompletionTokens += data.CompletionTokens
				usage.TotalTokens += data.TotalTokens
			}
		case schema.EventAgentEnd:
			if data, ok := event.Data.(schema.AgentEndData); ok {
				if textBuf.Len() == 0 && data.Message != "" {
					textBuf.WriteString(data.Message)
				}
			}
		}
	}

	if hasUsage {
		return textBuf.String(), &usage
	}

	return textBuf.String(), nil
}
