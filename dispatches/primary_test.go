package dispatches

import (
	"context"
	"io"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

// TestRun_UnifiedMode_ForwardsToPrimary verifies that when a Primary Assistant
// is attached, Dispatcher.Run bypasses fastPath + intent LLM + executeTask
// entirely and relays the request to the Primary.
func TestRun_UnifiedMode_ForwardsToPrimary(t *testing.T) {
	reg := newTestRegistry()

	chat := &stubAgent{id: "chat"}
	intentLLM := &countingChatCompleter{}

	primary := &stubAgent{
		id: "primary",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("hello back"),
				}, "primary"),
			},
		},
	}

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil, nil, nil,
		WithLLM(intentLLM, "test"),
		WithFallbackAgent(chat),
		WithPrimaryAssistant(primary),
	)

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("hi")}}

	resp, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if primary.ranCount() != 1 {
		t.Errorf("primary.ranCount = %d, want 1", primary.ranCount())
	}

	if chat.ranCount() != 0 {
		t.Errorf("chat.ranCount = %d, want 0 (fastPath/fallback must be skipped)", chat.ranCount())
	}

	if intentLLM.calls != 0 {
		t.Errorf("intent LLM calls = %d, want 0 (unified mode must skip intent phase)", intentLLM.calls)
	}

	if len(resp.Messages) == 0 || resp.Messages[0].Content.Text() != "hello back" {
		t.Errorf("response = %+v, want primary's text verbatim", resp.Messages)
	}
}

// TestRun_UnifiedMode_Disabled_ByDefault confirms the pre-M4 baseline: when
// no primary is attached, the dispatcher walks the classical pipeline.
func TestRun_UnifiedMode_Disabled_ByDefault(t *testing.T) {
	reg := newTestRegistry()
	chat := &stubAgent{id: "chat"}
	// No primary attached — fastPath / intent LLM should decide the path.

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil, nil, nil,
		WithFallbackAgent(chat),
		WithFastPath(DisabledFastPathConfig()),
		WithMaxRecursionDepth(0), // force fallbackRun path without LLM
	)

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("anything")}}

	if _, err := d.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if chat.ranCount() != 1 {
		t.Errorf("fallback chat.ranCount = %d, want 1 (classical path should run with no primary)", chat.ranCount())
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
		nil, nil, nil,
		WithFallbackAgent(chat),
	)

	primary := &stubAgent{id: "primary"}
	d.SetPrimaryAssistant(primary)

	if d.primaryAssistant == nil || d.primaryAssistant.ID() != "primary" {
		t.Fatalf("SetPrimaryAssistant did not attach the agent; got %v", d.primaryAssistant)
	}

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("x")}}
	if _, err := d.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if primary.ranCount() != 1 {
		t.Errorf("primary.ranCount = %d, want 1 after post-construction attach", primary.ranCount())
	}
}

// TestRunStream_UnifiedMode_EmitsUnifiedPrimaryPhase checks that the
// streaming path wraps Primary execution in a single EventPhaseStart/End
// pair labelled unified_primary (no intent/execute/summarize bracketing).
func TestRunStream_UnifiedMode_EmitsUnifiedPrimaryPhase(t *testing.T) {
	reg := newTestRegistry()
	chat := &stubAgent{id: "chat"}
	primary := &streamableStubAgent{stubAgent: stubAgent{id: "primary"}}

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil, nil, nil,
		WithFallbackAgent(chat),
		WithPrimaryAssistant(primary),
	)

	stream, err := d.RunStream(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("hello")},
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	defer func() { _ = stream.Close() }()

	phases := make([]string, 0, 4)

	for {
		ev, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			t.Fatalf("Recv: %v", recvErr)
		}

		switch ev.Type {
		case schema.EventPhaseStart:
			if data, ok := ev.Data.(schema.PhaseStartData); ok {
				phases = append(phases, "start:"+data.Phase)
			}
		case schema.EventPhaseEnd:
			if data, ok := ev.Data.(schema.PhaseEndData); ok {
				phases = append(phases, "end:"+data.Phase)
			}
		}
	}

	if len(phases) != 2 || phases[0] != "start:"+PrimaryPhase || phases[1] != "end:"+PrimaryPhase {
		t.Errorf("phase events = %v, want [start:%s end:%s]", phases, PrimaryPhase, PrimaryPhase)
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

// streamableStubAgent wraps stubAgent with a minimal RunStream so the
// streaming dispatcher path can exercise it. Implementation mirrors the
// production taskagent: emit a single text delta carrying the response,
// then close.
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
				Delta: resp.Messages[0].Content.Text(),
			})); err != nil {
				return err
			}
		}

		return nil
	}), nil
}
