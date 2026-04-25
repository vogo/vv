package dispatches

import (
	"context"
	"fmt"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/schema"
)

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
