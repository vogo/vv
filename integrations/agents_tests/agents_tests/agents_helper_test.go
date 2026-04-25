package agents_tests

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

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

// --- Helpers ---

// recordingStubAgent records the last request it received.
type recordingStubAgent struct {
	id          string
	response    *schema.RunResponse
	lastRequest *schema.RunRequest
	mu          sync.Mutex
}

var _ agent.Agent = (*recordingStubAgent)(nil)

func (s *recordingStubAgent) ID() string          { return s.id }
func (s *recordingStubAgent) Name() string        { return s.id }
func (s *recordingStubAgent) Description() string { return s.id }

func (s *recordingStubAgent) Run(_ context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	s.mu.Lock()
	s.lastRequest = req
	s.mu.Unlock()

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

// timingStubAgent records invocation timestamps for parallel execution testing.
type timingStubAgent struct {
	id       string
	response *schema.RunResponse
	startRef *atomic.Int64
	delay    time.Duration
}

var _ agent.Agent = (*timingStubAgent)(nil)

func (s *timingStubAgent) ID() string          { return s.id }
func (s *timingStubAgent) Name() string        { return s.id }
func (s *timingStubAgent) Description() string { return s.id }

func (s *timingStubAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	s.startRef.Store(time.Now().UnixNano())
	if s.delay > 0 {
		time.Sleep(s.delay)
	}

	return s.response, nil
}

// failingStubAgent always returns an error.
type failingStubAgent struct {
	id  string
	err error
}

var _ agent.Agent = (*failingStubAgent)(nil)

func (s *failingStubAgent) ID() string          { return s.id }
func (s *failingStubAgent) Name() string        { return s.id }
func (s *failingStubAgent) Description() string { return s.id }

func (s *failingStubAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return nil, s.err
}

// callbackStubAgent invokes a callback when Run is called.
type callbackStubAgent struct {
	id       string
	response *schema.RunResponse
	onRun    func()
}

var _ agent.Agent = (*callbackStubAgent)(nil)

func (s *callbackStubAgent) ID() string          { return s.id }
func (s *callbackStubAgent) Name() string        { return s.id }
func (s *callbackStubAgent) Description() string { return s.id }

func (s *callbackStubAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	if s.onRun != nil {
		s.onRun()
	}

	return s.response, nil
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
