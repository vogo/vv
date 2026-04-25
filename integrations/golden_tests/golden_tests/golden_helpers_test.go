package golden_tests

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/registries"
)

// Shared mock/stub types. These duplicate the primitives that live inside
// dispatches_tests because Go test helpers do not export across packages;
// keeping them private here also stops future edits to the dispatcher test
// helpers from silently changing the golden baseline.

type sequentialMockLLM struct {
	responses []*aimodel.ChatResponse
	callCount atomic.Int32
}

func (m *sequentialMockLLM) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	idx := int(m.callCount.Add(1)) - 1

	if idx < len(m.responses) {
		return m.responses[idx], nil
	}

	if len(m.responses) > 0 {
		return m.responses[len(m.responses)-1], nil
	}

	return &aimodel.ChatResponse{}, nil
}

func (m *sequentialMockLLM) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, fmt.Errorf("not implemented")
}

// callTrackingAgent records invocation state and lets individual cases pin a
// canned response.
type callTrackingAgent struct {
	id       string
	called   atomic.Bool
	runCount atomic.Int32
	response *schema.RunResponse
}

var _ agent.Agent = (*callTrackingAgent)(nil)

func (a *callTrackingAgent) ID() string          { return a.id }
func (a *callTrackingAgent) Name() string        { return a.id }
func (a *callTrackingAgent) Description() string { return a.id }

func (a *callTrackingAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	a.called.Store(true)
	a.runCount.Add(1)

	if a.response != nil {
		return a.response, nil
	}

	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("stub response from " + a.id),
			}, a.id),
		},
	}, nil
}

func newGoldenRegistry() *registries.Registry {
	reg := registries.New()

	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		reg.MustRegister(registries.AgentDescriptor{
			ID:           id,
			DisplayName:  id,
			Description:  id + " agent",
			Dispatchable: true,
		})
	}

	return reg
}

// intentJSONResponse wraps a classical intent-recognition JSON in a chat
// response. The routing LLM receives the intent system prompt and is expected
// to return exactly this JSON.
func intentJSONResponse(jsonBody string) *aimodel.ChatResponse {
	return &aimodel.ChatResponse{
		Choices: []aimodel.Choice{{
			Message: aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(jsonBody),
			},
			FinishReason: aimodel.FinishReasonStop,
		}},
		Usage: aimodel.Usage{PromptTokens: 50, CompletionTokens: 15},
	}
}

// primaryTextResponse is the Primary Assistant's "answer inline" shape — no
// tool call, just a final assistant message.
func primaryTextResponse(text string) *aimodel.ChatResponse {
	return &aimodel.ChatResponse{
		Choices: []aimodel.Choice{{
			Message: aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(text),
			},
			FinishReason: aimodel.FinishReasonStop,
		}},
		Usage: aimodel.Usage{PromptTokens: 60, CompletionTokens: 20},
	}
}

// primaryToolCallResponse builds a Primary tool-call message (delegate_to_* or
// plan_task). The dispatcher handler consumes the tool call; the next mock
// response is the Primary's next turn after the tool result is fed back in.
func primaryToolCallResponse(name, argsJSON string) *aimodel.ChatResponse {
	return &aimodel.ChatResponse{
		Choices: []aimodel.Choice{{
			Message: aimodel.Message{
				Role: aimodel.RoleAssistant,
				ToolCalls: []aimodel.ToolCall{{
					ID:   "tc_golden_" + name,
					Type: "function",
					Function: aimodel.FunctionCall{
						Name:      name,
						Arguments: argsJSON,
					},
				}},
			},
			FinishReason: aimodel.FinishReasonToolCalls,
		}},
		Usage: aimodel.Usage{PromptTokens: 80, CompletionTokens: 30},
	}
}
