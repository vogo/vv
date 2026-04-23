package dispatches_tests

import (
	"context"
	"fmt"
	"maps"
	"sync/atomic"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/registries"
)

// =============================================================================
// Mock / stub types (redefined locally since originals are in _test.go files)
// =============================================================================

// sequentialMockLLM returns different responses on successive calls.
type sequentialMockLLM struct {
	responses []*aimodel.ChatResponse
	errors    []error
	callCount atomic.Int32
}

func (m *sequentialMockLLM) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	idx := int(m.callCount.Add(1)) - 1
	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}

	if idx < len(m.responses) {
		return m.responses[idx], nil
	}

	// Default: return last response.
	if len(m.responses) > 0 {
		return m.responses[len(m.responses)-1], nil
	}

	return &aimodel.ChatResponse{}, nil
}

func (m *sequentialMockLLM) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, fmt.Errorf("not implemented")
}

// failingAgent always returns an error.
type failingAgent struct {
	id string
}

func (a *failingAgent) ID() string          { return a.id }
func (a *failingAgent) Name() string        { return a.id }
func (a *failingAgent) Description() string { return a.id }

func (a *failingAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return nil, fmt.Errorf("agent %s: simulated failure", a.id)
}

// callTrackingAgent records whether it was invoked.
type callTrackingAgent struct {
	id       string
	called   atomic.Bool
	response *schema.RunResponse
}

func (a *callTrackingAgent) ID() string          { return a.id }
func (a *callTrackingAgent) Name() string        { return a.id }
func (a *callTrackingAgent) Description() string { return a.id }

func (a *callTrackingAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	a.called.Store(true)

	if a.response != nil {
		return a.response, nil
	}

	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("response from " + a.id),
			}, a.id),
		},
	}, nil
}

// stubAgent is a minimal agent implementation for testing.
type stubAgent struct {
	id       string
	response *schema.RunResponse
	err      error
}

var _ agent.Agent = (*stubAgent)(nil)

func (s *stubAgent) ID() string          { return s.id }
func (s *stubAgent) Name() string        { return s.id }
func (s *stubAgent) Description() string { return s.id }

func (s *stubAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.response != nil {
		return s.response, nil
	}

	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("stub response from " + s.id),
			}, s.id),
		},
	}, nil
}

// stubStreamAgent implements agent.StreamAgent for testing.
type stubStreamAgent struct {
	id       string
	response string
}

var _ agent.StreamAgent = (*stubStreamAgent)(nil)

func (s *stubStreamAgent) ID() string          { return s.id }
func (s *stubStreamAgent) Name() string        { return s.id }
func (s *stubStreamAgent) Description() string { return s.id }

func (s *stubStreamAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(s.response),
			}, s.id),
		},
	}, nil
}

func (s *stubStreamAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, 8, func(ctx context.Context, send func(schema.Event) error) error {
		if err := send(schema.NewEvent(schema.EventAgentStart, s.id, req.SessionID, schema.AgentStartData{})); err != nil {
			return err
		}

		if err := send(schema.NewEvent(schema.EventTextDelta, s.id, req.SessionID, schema.TextDeltaData{Delta: s.response})); err != nil {
			return err
		}

		return send(schema.NewEvent(schema.EventAgentEnd, s.id, req.SessionID, schema.AgentEndData{
			Message: s.response,
		}))
	}), nil
}

// =============================================================================
// Test helpers for integration tests
// =============================================================================

func newIntegrationRegistry() *registries.Registry {
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

func makeSubAgents(agents map[string]agent.Agent) map[string]agent.Agent {
	defaults := map[string]agent.Agent{
		"coder":      &stubAgent{id: "coder"},
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	maps.Copy(defaults, agents)

	return defaults
}
