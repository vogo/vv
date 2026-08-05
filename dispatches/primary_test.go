package dispatches

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

// TestRun_UnifiedMode_ForwardsToPrimary verifies that the dispatcher relays
// the request straight to the Primary Assistant.
func TestRun_UnifiedMode_ForwardsToPrimary(t *testing.T) {
	reg := newTestRegistry()

	chat := &stubAgent{id: "chat"}

	primary := &stubAgent{
		id: "primary",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				func() schema.Message {
					m := schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleAssistant, "hello back")
					m.AgentID = "primary"
					return m
				}(),
			},
		},
	}

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil,
		WithFallbackAgent(chat),
		WithPrimaryAssistant(primary),
	)

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "hi")}}

	resp, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if primary.ranCount() != 1 {
		t.Errorf("primary.ranCount = %d, want 1", primary.ranCount())
	}

	if chat.ranCount() != 0 {
		t.Errorf("chat.ranCount = %d, want 0 (fallback must be skipped when primary present)", chat.ranCount())
	}

	if len(resp.Messages) == 0 || resp.Messages[0].Text() != "hello back" {
		t.Errorf("response = %+v, want primary's text verbatim", resp.Messages)
	}
}

// TestRun_NilPrimary_ReturnsError verifies that when no Primary is
// attached, Run must return an error rather than silently falling back to
// any classical pipeline.
func TestRun_NilPrimary_ReturnsError(t *testing.T) {
	reg := newTestRegistry()
	chat := &stubAgent{id: "chat"}

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil,
		WithFallbackAgent(chat),
	)

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "anything")}}

	_, err := d.Run(context.Background(), req)
	if err == nil {
		t.Fatal("Run with nil Primary must return error")
	}

	if !strings.Contains(err.Error(), "primary assistant required") {
		t.Errorf("error = %q, want substring %q", err.Error(), "primary assistant required")
	}

	if chat.ranCount() != 0 {
		t.Errorf("chat.ranCount = %d, want 0 (no classical fallback)", chat.ranCount())
	}
}

// TestRunStream_NilPrimary_ReturnsError mirrors TestRun_NilPrimary for the
// streaming path.
func TestRunStream_NilPrimary_ReturnsError(t *testing.T) {
	reg := newTestRegistry()
	chat := &stubAgent{id: "chat"}

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil,
		WithFallbackAgent(chat),
	)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "anything")},
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	var streamErr error
	for {
		_, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			streamErr = recvErr
			break
		}
	}

	if streamErr == nil {
		t.Fatal("RunStream with nil Primary must surface an error")
	}

	if !strings.Contains(streamErr.Error(), "primary assistant required") {
		t.Errorf("error = %q, want substring %q", streamErr.Error(), "primary assistant required")
	}
}

// TestSetPrimaryAssistant_PostConstruction_AttachesAgent verifies the
// post-construction setter — needed because setup.New must build the
// dispatcher first (so the Primary's plan_task tool can take a
// PlanExecutor handle on it) before building and attaching the Primary.
func TestSetPrimaryAssistant_PostConstruction_AttachesAgent(t *testing.T) {
	reg := newTestRegistry()
	chat := &stubAgent{id: "chat"}

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil,
		WithFallbackAgent(chat),
	)

	primary := &stubAgent{id: "primary"}
	d.SetPrimaryAssistant(primary)

	if d.primaryAssistant == nil || d.primaryAssistant.ID() != "primary" {
		t.Fatalf("SetPrimaryAssistant did not attach the agent; got %v", d.primaryAssistant)
	}

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "x")}}
	if _, err := d.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if primary.ranCount() != 1 {
		t.Errorf("primary.ranCount = %d, want 1 after post-construction attach", primary.ranCount())
	}
}

// TestRunStream_UnifiedMode_RelaysPrimaryEventsDirectly checks that the
// streaming path forwards Primary events without a unified_primary phase
// envelope or SubAgentStart/End wrapper.
func TestRunStream_UnifiedMode_RelaysPrimaryEventsDirectly(t *testing.T) {
	reg := newTestRegistry()
	chat := &stubAgent{id: "chat"}
	primary := &streamableStubAgent{stubAgent: stubAgent{id: "primary"}}

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil,
		WithFallbackAgent(chat),
		WithPrimaryAssistant(primary),
	)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "hello")},
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	var (
		phaseCount    int
		subAgentCount int
		hasTextDelta  bool
	)

	for {
		ev, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			t.Fatalf("Recv: %v", recvErr)
		}

		switch ev.Type {
		case schema.EventPhaseStart, schema.EventPhaseEnd:
			phaseCount++
		case schema.EventSubAgentStart, schema.EventSubAgentEnd:
			subAgentCount++
		case schema.EventTextDelta:
			hasTextDelta = true
		}
	}

	if phaseCount != 0 {
		t.Errorf("phase events = %d, want 0 (primary stream is relayed directly)", phaseCount)
	}

	if subAgentCount != 0 {
		t.Errorf("sub-agent events = %d, want 0 (primary is not wrapped as sub-agent)", subAgentCount)
	}

	if !hasTextDelta {
		t.Error("expected TextDelta from primary stream relay")
	}

	if primary.ranCount() != 1 {
		t.Errorf("primary.ranCount = %d, want 1", primary.ranCount())
	}
}

// TestRunPlan_ImplementsPlanExecutor locks the compile-time contract so a
// future refactor that accidentally changes the signature fails the build
// here instead of deep in a tool-handler closure.
func TestRunPlan_ImplementsPlanExecutor(_ *testing.T) {
	var _ PlanExecutor = (*Dispatcher)(nil)
}

// TestSetFallbackAgent_PostConstruction_AttachesAgent guards the setter
// used by setup.New to swap in a degraded-Primary fallback after the
// dispatcher has been constructed.
func TestSetFallbackAgent_PostConstruction_AttachesAgent(t *testing.T) {
	reg := newTestRegistry()
	chat := &stubAgent{id: "chat"}
	fallbackPrimary := &stubAgent{id: "primary-fallback"}

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil,
		WithFallbackAgent(chat),
	)

	d.SetFallbackAgent(fallbackPrimary)

	if d.fallbackAgent == nil || d.fallbackAgent.ID() != "primary-fallback" {
		t.Fatalf("SetFallbackAgent did not attach the agent; got %v", d.fallbackAgent)
	}
}

// TestRun_UnifiedMode_DepthExceeded_UsesPrimaryFallback guards the
// depth-exceed early-return: when a nested Dispatcher call recurses back to
// maxRecursionDepth, the configured fallback (a degraded Primary) becomes
// the entry point — never the classical chat agent.
func TestRun_UnifiedMode_DepthExceeded_UsesPrimaryFallback(t *testing.T) {
	reg := newTestRegistry()

	chat := &stubAgent{id: "chat"}
	primary := &stubAgent{id: "primary"}
	fallbackPrimary := &stubAgent{
		id: "primary-fallback",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				func() schema.Message {
					m := schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleAssistant, "fallback primary answer")
					m.AgentID = "primary-fallback"
					return m
				}(),
			},
		},
	}

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil,
		WithFallbackAgent(chat),
		WithPrimaryAssistant(primary),
		WithMaxRecursionDepth(1),
	)

	d.SetFallbackAgent(fallbackPrimary)

	ctx := IncrementDepth(context.Background())

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "nested")}}
	resp, err := d.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if fallbackPrimary.ranCount() != 1 {
		t.Errorf("fallback primary.ranCount = %d, want 1", fallbackPrimary.ranCount())
	}

	if chat.ranCount() != 0 {
		t.Errorf("chat.ranCount = %d, want 0 (must not fall back to chat in unified mode)", chat.ranCount())
	}

	if primary.ranCount() != 0 {
		t.Errorf("primary.ranCount = %d, want 0 (depth guard must short-circuit before primary branch)", primary.ranCount())
	}

	if len(resp.Messages) == 0 || resp.Messages[0].Text() != "fallback primary answer" {
		t.Errorf("response = %+v, want fallback primary answer", resp.Messages)
	}
}

// TestRunStream_DepthExceeded_RelaysFallbackDirectly verifies that when
// the recursion-depth fallback fires, the dispatcher relays the fallback
// agent stream without phase or sub-agent envelopes.
func TestRunStream_DepthExceeded_RelaysFallbackDirectly(t *testing.T) {
	reg := newTestRegistry()
	chat := &stubAgent{id: "chat"}
	primary := &stubAgent{id: "primary"}
	fallbackPrimary := &streamableStubAgent{stubAgent: stubAgent{id: "primary-fallback"}}

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil,
		WithFallbackAgent(chat),
		WithPrimaryAssistant(primary),
		WithMaxRecursionDepth(1),
	)
	d.SetFallbackAgent(fallbackPrimary)

	ctx := IncrementDepth(context.Background())

	stream, err := d.RunStream(ctx, &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "nested")},
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	var (
		phaseCount    int
		subAgentCount int
		hasTextDelta  bool
	)

	for {
		ev, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			t.Fatalf("Recv: %v", recvErr)
		}

		switch ev.Type {
		case schema.EventPhaseStart, schema.EventPhaseEnd:
			phaseCount++
		case schema.EventSubAgentStart, schema.EventSubAgentEnd:
			subAgentCount++
		case schema.EventTextDelta:
			hasTextDelta = true
		}
	}

	if phaseCount != 0 {
		t.Errorf("phase events = %d, want 0 on fallback path", phaseCount)
	}

	if subAgentCount != 0 {
		t.Errorf("sub-agent events = %d, want 0 on fallback path", subAgentCount)
	}

	if !hasTextDelta {
		t.Error("expected TextDelta from fallback stream relay")
	}

	if fallbackPrimary.ranCount() != 1 {
		t.Errorf("fallback primary.ranCount = %d, want 1", fallbackPrimary.ranCount())
	}
}

// streamableStubAgent wraps stubAgent with a minimal RunStream so the
// streaming dispatcher path can exercise it.
type streamableStubAgent struct {
	stubAgent
}

var _ agent.StreamAgent = (*streamableStubAgent)(nil)

func (s *streamableStubAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, agent.DefaultStreamBufferSize, func(_ context.Context, send func(schema.Event) error) error {
		resp, err := s.Run(ctx, req)
		if err != nil {
			return err
		}

		if len(resp.Messages) > 0 {
			if err := send(schema.NewEvent(schema.EventTextDelta, s.id, req.SessionID, schema.TextDeltaData{
				Delta: resp.Messages[0].Text(),
			})); err != nil {
				return err
			}
		}

		return nil
	}), nil
}
