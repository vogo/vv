package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

// classify calls the planner sub-agent to classify/plan the request.
func (d *Dispatcher) classify(ctx context.Context, req *schema.RunRequest, contextSummary string) (*ClassifyResult, *aimodel.Usage, error) {
	if d.plannerAgent == nil {
		return d.classifyDirect(ctx, req)
	}

	// Build planner request with context.
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

	plannerReq := &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
	}

	resp, err := d.plannerAgent.Run(ctx, plannerReq)
	if err != nil {
		return nil, nil, fmt.Errorf("planner run: %w", err)
	}

	usage := resp.Usage

	if len(resp.Messages) == 0 {
		return nil, usage, fmt.Errorf("empty planner response")
	}

	text := resp.Messages[0].Content.Text()
	jsonStr := extractJSON(text)

	var result ClassifyResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, usage, fmt.Errorf("parse planner JSON: %w", err)
	}

	if err := result.validate(d.registry, d.subAgents); err != nil {
		return nil, usage, err
	}

	return &result, usage, nil
}

// classifyStream calls the planner using streaming.
func (d *Dispatcher) classifyStream(
	ctx context.Context,
	req *schema.RunRequest,
	contextSummary string,
	send func(schema.Event) error,
) (*ClassifyResult, *aimodel.Usage, error) {
	if d.plannerAgent == nil {
		return d.classifyDirect(ctx, req)
	}

	// Try streaming path.
	sa, ok := d.plannerAgent.(agent.StreamAgent)
	if !ok {
		// Fallback to non-streaming.
		return d.classify(ctx, req, contextSummary)
	}

	// Build planner request with context (same as classify).
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

	plannerReq := &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
	}

	stream, err := sa.RunStream(ctx, plannerReq)
	if err != nil {
		return nil, nil, fmt.Errorf("planner stream: %w", err)
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

			if hasUsage {
				return nil, &usage, fmt.Errorf("planner stream recv: %w", recvErr)
			}

			return nil, nil, fmt.Errorf("planner stream recv: %w", recvErr)
		}

		switch event.Type {
		case schema.EventTextDelta:
			if data, ok := event.Data.(schema.TextDeltaData); ok {
				textBuf.WriteString(data.Delta)
			}
		case schema.EventToolCallStart, schema.EventToolResult, schema.EventError:
			if err := send(event); err != nil {
				slog.Warn("orchestrator: planner stream send error", "error", err)
			}
		case schema.EventLLMCallEnd:
			if data, ok := event.Data.(schema.LLMCallEndData); ok {
				hasUsage = true
				usage.PromptTokens += data.PromptTokens
				usage.CompletionTokens += data.CompletionTokens
				usage.TotalTokens += data.TotalTokens
			}
		}
	}

	var usagePtr *aimodel.Usage
	if hasUsage {
		usagePtr = &usage
	}

	text := textBuf.String()
	jsonStr := extractJSON(text)

	var result ClassifyResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, usagePtr, fmt.Errorf("parse planner JSON: %w", err)
	}

	if err := result.validate(d.registry, d.subAgents); err != nil {
		return nil, usagePtr, err
	}

	return &result, usagePtr, nil
}

// classifyDirect makes a direct LLM call to classify the task (fallback when no planner agent).
func (d *Dispatcher) classifyDirect(ctx context.Context, req *schema.RunRequest) (*ClassifyResult, *aimodel.Usage, error) {
	systemPrompt := d.plannerSystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a task planner. Respond with JSON: {\"mode\": \"direct\", \"agent\": \"chat\"}"
	}

	systemPrompt = strings.Replace(systemPrompt, "{{.WorkingDir}}", d.workingDir, 1)

	msgs := make([]aimodel.Message, 0, len(req.Messages)+1)
	msgs = append(msgs, aimodel.Message{
		Role:    aimodel.RoleSystem,
		Content: aimodel.NewTextContent(systemPrompt),
	})
	msgs = append(msgs, schema.ToAIModelMessages(req.Messages)...)

	chatReq := &aimodel.ChatRequest{
		Model:    d.model,
		Messages: msgs,
	}

	resp, err := d.llm.ChatCompletion(ctx, chatReq)
	if err != nil {
		return nil, nil, err
	}

	usage := &resp.Usage

	if len(resp.Choices) == 0 {
		return nil, usage, fmt.Errorf("empty classification response")
	}

	text := resp.Choices[0].Message.Content.Text()

	jsonStr := extractJSON(text)

	var result ClassifyResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, usage, fmt.Errorf("parse classification JSON: %w", err)
	}

	if err := result.validate(d.registry, d.subAgents); err != nil {
		return nil, usage, err
	}

	return &result, usage, nil
}
