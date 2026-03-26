package cli

import (
	"context"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/routeragent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vagents/vaga/config"
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

func TestSelectAgent(t *testing.T) {
	coder := &stubStreamAgent{id: "coder", response: "code response"}
	chat := &stubStreamAgent{id: "chat", response: "chat response"}

	routes := []routeragent.Route{
		{Agent: coder, Description: "Handles code tasks"},
		{Agent: chat, Description: "Handles general conversation"},
	}

	// routeFn that always selects index 0 (coder).
	routeFn := func(_ context.Context, _ *schema.RunRequest, routes []routeragent.Route) (*routeragent.RouteResult, error) {
		return &routeragent.RouteResult{Agent: routes[0].Agent}, nil
	}

	app := New(routeFn, routes, coder, chat, &config.Config{})

	req := &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("write code")},
	}

	sa, err := app.selectAgent(context.Background(), req)
	if err != nil {
		t.Fatalf("selectAgent: %v", err)
	}

	if sa.ID() != "coder" {
		t.Errorf("selected agent ID = %q, want %q", sa.ID(), "coder")
	}
}

func TestSelectAgent_Chat(t *testing.T) {
	coder := &stubStreamAgent{id: "coder", response: "code response"}
	chat := &stubStreamAgent{id: "chat", response: "chat response"}

	routes := []routeragent.Route{
		{Agent: coder, Description: "Handles code tasks"},
		{Agent: chat, Description: "Handles general conversation"},
	}

	// routeFn that always selects index 1 (chat).
	routeFn := func(_ context.Context, _ *schema.RunRequest, routes []routeragent.Route) (*routeragent.RouteResult, error) {
		return &routeragent.RouteResult{Agent: routes[1].Agent}, nil
	}

	app := New(routeFn, routes, coder, chat, &config.Config{})

	req := &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("tell me a joke")},
	}

	sa, err := app.selectAgent(context.Background(), req)
	if err != nil {
		t.Fatalf("selectAgent: %v", err)
	}

	if sa.ID() != "chat" {
		t.Errorf("selected agent ID = %q, want %q", sa.ID(), "chat")
	}
}

func TestMultiTurnHistory(t *testing.T) {
	coder := &stubStreamAgent{id: "coder", response: "response"}
	chat := &stubStreamAgent{id: "chat", response: "response"}

	routes := []routeragent.Route{
		{Agent: coder, Description: "code"},
		{Agent: chat, Description: "chat"},
	}

	routeFn := func(_ context.Context, _ *schema.RunRequest, routes []routeragent.Route) (*routeragent.RouteResult, error) {
		return &routeragent.RouteResult{Agent: routes[0].Agent}, nil
	}

	app := New(routeFn, routes, coder, chat, &config.Config{})

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
	coder := &stubStreamAgent{id: "coder", response: "code"}
	chat := &stubStreamAgent{id: "chat", response: "chat"}

	routes := []routeragent.Route{
		{Agent: coder, Description: "code"},
		{Agent: chat, Description: "chat"},
	}

	routeFn := func(_ context.Context, _ *schema.RunRequest, _ []routeragent.Route) (*routeragent.RouteResult, error) {
		return nil, nil
	}

	cfg := &config.Config{Mode: "cli"}

	app := New(routeFn, routes, coder, chat, cfg)

	if app == nil {
		t.Fatal("New returned nil")
	}

	if app.coder.ID() != "coder" {
		t.Errorf("coder ID = %q, want %q", app.coder.ID(), "coder")
	}

	if app.chat.ID() != "chat" {
		t.Errorf("chat ID = %q, want %q", app.chat.ID(), "chat")
	}

	if app.cfg != cfg {
		t.Error("cfg not stored correctly")
	}

	if len(app.routes) != 2 {
		t.Errorf("routes len = %d, want 2", len(app.routes))
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
