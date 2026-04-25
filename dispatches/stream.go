package dispatches

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

// forwardSubAgentStream runs a sub-agent and forwards its stream events, wrapping with
// SubAgentStart/End events that include execution stats.
func (d *Dispatcher) forwardSubAgentStream(
	ctx context.Context,
	send func(schema.Event) error,
	subAgent agent.Agent,
	req *schema.RunRequest,
	agentName string,
	stepID string,
	sessionID string,
) error {
	if subAgent == nil {
		return fmt.Errorf("orchestrator: no agent available for %q", agentName)
	}

	if err := send(schema.NewEvent(schema.EventSubAgentStart, d.ID(), sessionID, schema.SubAgentStartData{
		AgentName: agentName,
		StepID:    stepID,
	})); err != nil {
		return err
	}

	start := time.Now()
	toolCalls := 0
	promptTokens := 0
	completionTokens := 0

	// Try streaming first, fall back to non-streaming.
	sa, isStream := subAgent.(agent.StreamAgent)
	if isStream {
		stream, err := sa.RunStream(ctx, req)
		if err != nil {
			return err
		}

		defer func() { _ = stream.Close() }()

		for {
			event, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}

				return err
			}

			// Track stats from forwarded events.
			switch event.Type {
			case schema.EventToolCallStart:
				toolCalls++
			case schema.EventLLMCallEnd:
				if data, ok := event.Data.(schema.LLMCallEndData); ok {
					promptTokens += data.PromptTokens
					completionTokens += data.CompletionTokens
				}
			}

			if err := send(event); err != nil {
				return err
			}
		}
	} else {
		resp, err := subAgent.Run(ctx, req)
		if err != nil {
			return err
		}

		if resp.Usage != nil {
			promptTokens = resp.Usage.PromptTokens
			completionTokens = resp.Usage.CompletionTokens
		}

		// Emit the response text as a single TextDelta + AgentEnd.
		if len(resp.Messages) > 0 {
			text := resp.Messages[0].Content.Text()
			if text != "" {
				if err := send(schema.NewEvent(schema.EventTextDelta, subAgent.ID(), sessionID, schema.TextDeltaData{Delta: text})); err != nil {
					return err
				}
			}

			if err := send(schema.NewEvent(schema.EventAgentEnd, subAgent.ID(), sessionID, schema.AgentEndData{
				Duration: time.Since(start).Milliseconds(),
				Message:  text,
			})); err != nil {
				return err
			}
		}
	}

	return send(schema.NewEvent(schema.EventSubAgentEnd, d.ID(), sessionID, schema.SubAgentEndData{
		AgentName:        agentName,
		StepID:           stepID,
		Duration:         time.Since(start).Milliseconds(),
		ToolCalls:        toolCalls,
		TokensUsed:       promptTokens + completionTokens,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
	}))
}
