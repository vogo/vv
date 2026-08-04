package project_instructions_tests

import (
	"context"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/largemodel"
	"github.com/vogo/vage/schema"
)

// mockChatCompleter is a simple mock for testing.
type mockChatCompleter struct {
	response *largemodel.Response
	err      error
}

func (m *mockChatCompleter) Protocol() schema.Protocol { return schema.ProtocolOpenAIChat }

func (m *mockChatCompleter) Call(_ context.Context, _ *largemodel.Request) (*largemodel.Response, error) {
	if m.err != nil {
		return nil, m.err
	}

	return m.response, nil
}

func (m *mockChatCompleter) CallStream(_ context.Context, _ *largemodel.Request) (*largemodel.Stream, error) {
	return nil, m.err
}

// captureChatCompleter captures the ChatRequest sent to it.
type captureChatCompleter struct {
	captured *largemodel.Request
	response *largemodel.Response
}

func (c *captureChatCompleter) Protocol() schema.Protocol { return schema.ProtocolOpenAIChat }

func (c *captureChatCompleter) Call(_ context.Context, req *largemodel.Request) (*largemodel.Response, error) {
	c.captured = req

	return c.response, nil
}

func (c *captureChatCompleter) CallStream(_ context.Context, _ *largemodel.Request) (*largemodel.Stream, error) {
	return nil, nil
}

// stubAgent is a minimal agent implementation for testing.
type stubAgent struct {
	id       string
	response *schema.RunResponse
}

var _ agent.Agent = (*stubAgent)(nil)

func (s *stubAgent) ID() string                { return s.id }
func (s *stubAgent) Name() string              { return s.id }
func (s *stubAgent) Description() string       { return s.id }
func (s *stubAgent) Protocol() schema.Protocol { return schema.ProtocolOpenAIChat }

func (s *stubAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
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
