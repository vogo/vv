package dispatches_tests

import (
	"context"
	"fmt"
	"maps"
	"sync/atomic"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/largemodel"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/registries"
)

// =============================================================================
// Mock / stub types (redefined locally since originals are in _test.go files)
// =============================================================================

// sequentialMockLLM returns different responses on successive calls.
type sequentialMockLLM struct {
	responses []*largemodel.Response
	errors    []error
	callCount atomic.Int32
}

func (m *sequentialMockLLM) Protocol() schema.Protocol { return schema.ProtocolOpenAIChat }

func (m *sequentialMockLLM) Call(_ context.Context, _ *largemodel.Request) (*largemodel.Response, error) {
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

	return &largemodel.Response{}, nil
}

func (m *sequentialMockLLM) CallStream(_ context.Context, _ *largemodel.Request) (*largemodel.Stream, error) {
	return nil, fmt.Errorf("not implemented")
}

// callTrackingAgent records whether it was invoked.
type callTrackingAgent struct {
	id       string
	called   atomic.Bool
	response *schema.RunResponse
}

func (a *callTrackingAgent) ID() string                { return a.id }
func (a *callTrackingAgent) Name() string              { return a.id }
func (a *callTrackingAgent) Description() string       { return a.id }
func (a *callTrackingAgent) Protocol() schema.Protocol { return schema.ProtocolOpenAIChat }

func (a *callTrackingAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	a.called.Store(true)

	if a.response != nil {
		return a.response, nil
	}

	return &schema.RunResponse{
		Messages: []schema.Message{
			func() schema.Message {
				m := schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleAssistant, "response from "+a.id)
				m.AgentID = a.id
				return m
			}(),
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

func (s *stubAgent) ID() string                { return s.id }
func (s *stubAgent) Name() string              { return s.id }
func (s *stubAgent) Description() string       { return s.id }
func (s *stubAgent) Protocol() schema.Protocol { return schema.ProtocolOpenAIChat }

func (s *stubAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.response != nil {
		return s.response, nil
	}

	return &schema.RunResponse{
		Messages: []schema.Message{
			func() schema.Message {
				m := schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleAssistant, "stub response from "+s.id)
				m.AgentID = s.id
				return m
			}(),
		},
	}, nil
}

// stubStreamAgent implements agent.StreamAgent for testing.
type stubStreamAgent struct {
	id       string
	response string
}

var _ agent.StreamAgent = (*stubStreamAgent)(nil)

func (s *stubStreamAgent) ID() string                { return s.id }
func (s *stubStreamAgent) Name() string              { return s.id }
func (s *stubStreamAgent) Description() string       { return s.id }
func (s *stubStreamAgent) Protocol() schema.Protocol { return schema.ProtocolOpenAIChat }

func (s *stubStreamAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{
		Messages: []schema.Message{
			func() schema.Message {
				m := schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleAssistant, s.response)
				m.AgentID = s.id
				return m
			}(),
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
