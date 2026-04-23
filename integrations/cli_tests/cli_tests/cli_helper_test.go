package cli_tests

import (
	"context"
	"fmt"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
)

// --- Helpers for CLI integration tests ---

// stubStreamAgent implements agent.StreamAgent for CLI integration testing.
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
	return schema.NewRunStream(ctx, 8, func(_ context.Context, send func(schema.Event) error) error {
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

// =============================================================================
// Helper: mock summarizer for compression tests
// =============================================================================

func compressionMockSummarizer() memory.Summarizer {
	return func(_ context.Context, msgs []schema.Message) (string, error) {
		return fmt.Sprintf("Summary of %d messages covering the conversation so far.", len(msgs)), nil
	}
}

// =============================================================================
// Mock tool registry for truncation tests
// =============================================================================

type mockToolRegistry struct {
	tools     map[string]schema.ToolDef
	executeFn func(ctx context.Context, name, args string) (schema.ToolResult, error)
}

func (m *mockToolRegistry) Register(def schema.ToolDef, _ tool.ToolHandler) error {
	if m.tools == nil {
		m.tools = make(map[string]schema.ToolDef)
	}
	m.tools[def.Name] = def
	return nil
}

func (m *mockToolRegistry) Unregister(name string) error {
	delete(m.tools, name)
	return nil
}

func (m *mockToolRegistry) Get(name string) (schema.ToolDef, bool) {
	d, ok := m.tools[name]
	return d, ok
}

func (m *mockToolRegistry) List() []schema.ToolDef {
	var defs []schema.ToolDef
	for _, d := range m.tools {
		defs = append(defs, d)
	}
	return defs
}

func (m *mockToolRegistry) Merge(defs []schema.ToolDef) {
	for _, d := range defs {
		m.tools[d.Name] = d
	}
}

func (m *mockToolRegistry) Execute(ctx context.Context, name, args string) (schema.ToolResult, error) {
	if m.executeFn != nil {
		return m.executeFn(ctx, name, args)
	}
	return schema.ToolResult{}, nil
}
