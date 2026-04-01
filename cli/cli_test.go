package cli

import (
	"context"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
)

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

func TestMultiTurnHistory(t *testing.T) {
	orchestrator := &stubStreamAgent{id: "orchestrator", response: "response"}

	app := New(orchestrator, &configs.Config{}, nil)

	// Simulate adding messages to history.
	app.history = append(app.history, schema.NewUserMessage("first message"))
	app.history = append(app.history, schema.NewAssistantMessage(
		aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent("first response")},
		"coder",
	))
	app.history = append(app.history, schema.NewUserMessage("second message"))

	if len(app.history) != 3 {
		t.Errorf("history len = %d, want 3", len(app.history))
	}

	// Verify the messages are correct.
	if app.history[0].Content.Text() != "first message" {
		t.Errorf("history[0] = %q, want %q", app.history[0].Content.Text(), "first message")
	}

	if app.history[1].Content.Text() != "first response" {
		t.Errorf("history[1] = %q, want %q", app.history[1].Content.Text(), "first response")
	}

	if app.history[2].Content.Text() != "second message" {
		t.Errorf("history[2] = %q, want %q", app.history[2].Content.Text(), "second message")
	}
}

func TestDisplayMessageRendering(t *testing.T) {
	tests := []struct {
		role    string
		content string
	}{
		{"user", "hello"},
		{"agent", "response"},
		{"system", "info"},
		{"tool", "tool output"},
		{"error", "something went wrong"},
	}

	for _, tt := range tests {
		msg := DisplayMessage{
			Role:    tt.role,
			Content: tt.content,
		}

		if msg.Role != tt.role {
			t.Errorf("role = %q, want %q", msg.Role, tt.role)
		}

		if msg.Content != tt.content {
			t.Errorf("content = %q, want %q", msg.Content, tt.content)
		}
	}
}

func TestNewApp(t *testing.T) {
	orchestrator := &stubStreamAgent{id: "orchestrator", response: "response"}

	cfg := &configs.Config{Mode: "cli"}

	app := New(orchestrator, cfg, nil)

	if app == nil {
		t.Fatal("New returned nil")
	}

	if app.cfg != cfg {
		t.Error("cfg not stored correctly")
	}
}

func TestToolDepth_NoSubAgent(t *testing.T) {
	m := &model{nestingDepth: 0}
	if got := m.toolDepth(); got != 1 {
		t.Errorf("toolDepth() with nestingDepth 0 = %d, want 1", got)
	}
}

func TestToolDepth_InsideSubAgent(t *testing.T) {
	m := &model{nestingDepth: 1}
	if got := m.toolDepth(); got != 2 {
		t.Errorf("toolDepth() with nestingDepth 1 = %d, want 2", got)
	}
}

func TestNestingDepth_SubAgentLifecycle(t *testing.T) {
	orchestrator := &stubStreamAgent{id: "orchestrator", response: "response"}
	app := New(orchestrator, &configs.Config{}, nil)
	m := newModel(app, context.Background())

	// Initially depth is 0.
	if m.nestingDepth != 0 {
		t.Fatalf("initial nestingDepth = %d, want 0", m.nestingDepth)
	}

	// Simulate SubAgentStart event.
	startEvent := schema.NewEvent(schema.EventSubAgentStart, "test", "sess", schema.SubAgentStartData{
		AgentName:  "coder",
		StepID:     "step1",
		StepIndex:  1,
		TotalSteps: 1,
	})
	m.handleStreamEvent(streamEventMsg{event: startEvent})

	if m.nestingDepth != 1 {
		t.Errorf("after SubAgentStart nestingDepth = %d, want 1", m.nestingDepth)
	}

	// Tool depth should be 2 inside sub-agent.
	if got := m.toolDepth(); got != 2 {
		t.Errorf("toolDepth() inside sub-agent = %d, want 2", got)
	}

	// Simulate SubAgentEnd event.
	endEvent := schema.NewEvent(schema.EventSubAgentEnd, "test", "sess", schema.SubAgentEndData{
		AgentName: "coder",
		StepID:    "step1",
		Duration:  1000,
	})
	m.handleStreamEvent(streamEventMsg{event: endEvent})

	if m.nestingDepth != 0 {
		t.Errorf("after SubAgentEnd nestingDepth = %d, want 0", m.nestingDepth)
	}
}

func TestSessionStatus(t *testing.T) {
	// Verify status constants are distinct.
	statuses := []sessionStatus{statusIdle, statusProcessing, statusConfirming, statusQuitting}
	seen := make(map[sessionStatus]bool)

	for _, s := range statuses {
		if seen[s] {
			t.Errorf("duplicate status value: %d", s)
		}
		seen[s] = true
	}
}
