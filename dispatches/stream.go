package dispatches

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/orchestrate"
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

// streamPlan executes a plan's steps and emits sub-agent lifecycle events for each step.
// Returns the DAG result's aggregated usage (may be nil) and any error.
func (d *Dispatcher) streamPlan(
	ctx context.Context,
	send func(schema.Event) error,
	req *schema.RunRequest,
	plan *Plan,
	contextSummary string,
	sessionID string,
) (*aimodel.Usage, error) {
	nodes, err := d.buildNodes(plan, req, contextSummary)
	if err != nil {
		slog.Warn("orchestrator: DAG build failed, falling back to chat stream", "error", err)

		return nil, d.forwardSubAgentStream(ctx, send, d.fallbackAgent, req, "chat", "", sessionID)
	}

	totalSteps := len(plan.Steps)

	// Build a step index map for progress display.
	stepIndex := make(map[string]int, totalSteps)
	stepAgent := make(map[string]string, totalSteps)
	stepDesc := make(map[string]string, totalSteps)

	for i, step := range plan.Steps {
		stepIndex[step.ID] = i + 1
		stepAgent[step.ID] = step.Agent
		stepDesc[step.ID] = step.Description
	}

	// Use the DAG EventHandler to emit sub-agent lifecycle events.
	dagCfg := orchestrate.DAGConfig{
		MaxConcurrency: d.maxConcurrency,
		ErrorStrategy:  orchestrate.Skip,
		Aggregator:     &PlanAggregator{Summarizer: d.planGen},
		EventHandler: &streamingDAGHandler{
			send:       send,
			agentID:    d.ID(),
			sessionID:  sessionID,
			stepIndex:  stepIndex,
			totalSteps: totalSteps,
			stepAgent:  stepAgent,
			stepDesc:   stepDesc,
			startTimes: make(map[string]time.Time),
		},
	}

	result, err := orchestrate.ExecuteDAG(ctx, dagCfg, nodes, req)
	if err != nil {
		return nil, err
	}

	// Emit the final aggregated output.
	if result.FinalOutput != nil && len(result.FinalOutput.Messages) > 0 {
		text := result.FinalOutput.Messages[0].Content.Text()
		if text != "" {
			if err := send(schema.NewEvent(schema.EventTextDelta, d.ID(), sessionID, schema.TextDeltaData{Delta: text})); err != nil {
				return result.Usage, err
			}

			return result.Usage, send(schema.NewEvent(schema.EventAgentEnd, d.ID(), sessionID, schema.AgentEndData{
				Duration: 0,
				Message:  text,
			}))
		}
	}

	return result.Usage, nil
}

// streamingDAGHandler implements orchestrate.DAGEventHandler to emit sub-agent events.
type streamingDAGHandler struct {
	send       func(schema.Event) error
	agentID    string
	sessionID  string
	stepIndex  map[string]int
	totalSteps int
	stepAgent  map[string]string
	stepDesc   map[string]string
	startTimes map[string]time.Time
	mu         sync.Mutex
}

func (h *streamingDAGHandler) OnNodeStart(nodeID string) {
	h.mu.Lock()
	h.startTimes[nodeID] = time.Now()
	h.mu.Unlock()

	_ = h.send(schema.NewEvent(schema.EventSubAgentStart, h.agentID, h.sessionID, schema.SubAgentStartData{
		AgentName:   h.stepAgent[nodeID],
		StepID:      nodeID,
		Description: h.stepDesc[nodeID],
		StepIndex:   h.stepIndex[nodeID],
		TotalSteps:  h.totalSteps,
	}))
}

func (h *streamingDAGHandler) OnNodeComplete(nodeID string, status orchestrate.NodeStatus, err error) {
	h.mu.Lock()
	start := h.startTimes[nodeID]
	h.mu.Unlock()

	duration := int64(0)
	if !start.IsZero() {
		duration = time.Since(start).Milliseconds()
	}

	_ = h.send(schema.NewEvent(schema.EventSubAgentEnd, h.agentID, h.sessionID, schema.SubAgentEndData{
		AgentName: h.stepAgent[nodeID],
		StepID:    nodeID,
		Duration:  duration,
	}))
}

func (h *streamingDAGHandler) OnCheckpointError(_ string, _ error) {}
