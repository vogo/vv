package project_instructions_tests

import (
	"context"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

// mockChatCompleter is a simple mock for testing.
type mockChatCompleter struct {
	response *aimodel.ChatResponse
	err      error
}

func (m *mockChatCompleter) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}

	return m.response, nil
}

func (m *mockChatCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, m.err
}

// captureChatCompleter captures the ChatRequest sent to it.
type captureChatCompleter struct {
	captured *aimodel.ChatRequest
	response *aimodel.ChatResponse
}

func (c *captureChatCompleter) ChatCompletion(_ context.Context, req *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	c.captured = req

	return c.response, nil
}

func (c *captureChatCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, nil
}

// stubAgent is a minimal agent implementation for testing.
type stubAgent struct {
	id       string
	response *schema.RunResponse
}

var _ agent.Agent = (*stubAgent)(nil)

func (s *stubAgent) ID() string          { return s.id }
func (s *stubAgent) Name() string        { return s.id }
func (s *stubAgent) Description() string { return s.id }

func (s *stubAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
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
