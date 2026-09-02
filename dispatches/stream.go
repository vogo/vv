package dispatches

import (
	"context"
	"fmt"
	"time"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

// relayAgentStream runs an agent and forwards its stream events unchanged.
// Used for Primary and Fallback Primary — the user-facing entry points that
// should not be wrapped in SubAgentStart/End or phase envelopes.
func relayAgentStream(
	ctx context.Context,
	send func(schema.Event) error,
	ag agent.Agent,
	req *schema.RunRequest,
) error {
	if ag == nil {
		return fmt.Errorf("orchestrator: no agent available")
	}

	sa, isStream := ag.(agent.StreamAgent)
	if isStream {
		stream, err := sa.RunStream(ctx, req)
		if err != nil {
			return err
		}

		return stream.ForEach(send)
	}

	start := time.Now()
	resp, err := ag.Run(ctx, req)
	if err != nil {
		return err
	}

	if len(resp.Messages) > 0 {
		text := resp.Messages[0].Text()
		if text != "" {
			if err := send(schema.NewEvent(schema.EventTextDelta, ag.ID(), req.SessionID, schema.TextDeltaData{Delta: text})); err != nil {
				return err
			}
		}

		if err := send(schema.NewEvent(schema.EventAgentEnd, ag.ID(), req.SessionID, schema.AgentEndData{
			Duration: time.Since(start).Milliseconds(),
			Message:  text,
		})); err != nil {
			return err
		}
	}

	return nil
}
