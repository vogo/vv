package dispatches

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
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

// TestRunStream_UnifiedMode_LegacyShim_EmitsIntentAndExecutePhases guards
// the M5 G2 shim: with WithLegacyPhaseEvents(true) the unified stream must
// emit [start:intent, end:intent, start:execute, end:execute] and no
// unified_primary pair at all, so HTTP/CLI consumers that pinned to the
// pre-M5 event shape keep working without a code change.
func TestRunStream_UnifiedMode_LegacyShim_EmitsIntentAndExecutePhases(t *testing.T) {
	reg := newTestRegistry()
	chat := &stubAgent{id: "chat"}
	primary := &streamableStubAgent{stubAgent: stubAgent{id: "primary"}}

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil, nil, nil,
		WithFallbackAgent(chat),
		WithPrimaryAssistant(primary),
		WithLegacyPhaseEvents(true),
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

	want := []string{"start:intent", "end:intent", "start:execute", "end:execute"}
	if len(phases) != len(want) {
		t.Fatalf("phase events = %v, want %v", phases, want)
	}

	for i, p := range phases {
		if p != want[i] {
			t.Errorf("phase[%d] = %q, want %q (full: %v)", i, p, want[i], phases)
		}
	}

	for _, p := range phases {
		if p == "start:"+PrimaryPhase || p == "end:"+PrimaryPhase {
			t.Errorf("legacy shim must NOT emit %q: %v", PrimaryPhase, phases)
		}
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

// TestWithLegacyPhaseEvents_DeprecationWarn pins the M6 deprecation signal:
// WithLegacyPhaseEvents(true) emits a slog.Warn at Option-application time
// so operators see the migration notice in the logs once per Dispatcher
// construction; passing false stays silent so existing zero-config setups
// produce no extra noise.
func TestWithLegacyPhaseEvents_DeprecationWarn(t *testing.T) {
	cases := []struct {
		name      string
		enabled   bool
		wantWarn  bool
		wantField string
	}{
		{name: "enabled emits deprecation warn", enabled: true, wantWarn: true, wantField: "deprecated"},
		{name: "disabled stays silent", enabled: false, wantWarn: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer

			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

			t.Cleanup(func() { slog.SetDefault(prev) })

			d := New(newTestRegistry(), map[string]agent.Agent{}, nil, nil, nil, WithLegacyPhaseEvents(tc.enabled))
			if d == nil {
				t.Fatal("New returned nil")
			}

			out := buf.String()
			gotWarn := strings.Contains(out, tc.wantField) && strings.Contains(out, "level=WARN")

			if tc.wantWarn && !gotWarn {
				t.Errorf("expected deprecation warn containing %q in log; got %q", tc.wantField, out)
			}

			if !tc.wantWarn && out != "" {
				t.Errorf("expected silent (disabled), got log output: %q", out)
			}
		})
	}
}

// TestSetFallbackAgent_PostConstruction_AttachesAgent guards the M5 G3
// setter: unified mode needs to swap the pre-M5 chat fallback for a
// Primary-persona fallback after the dispatcher has been constructed (the
// Primary itself needs a PlanExecutor handle on the dispatcher).
func TestSetFallbackAgent_PostConstruction_AttachesAgent(t *testing.T) {
	reg := newTestRegistry()
	chat := &stubAgent{id: "chat"}
	fallbackPrimary := &stubAgent{id: "primary-fallback"}

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil, nil, nil,
		WithFallbackAgent(chat),
	)

	d.SetFallbackAgent(fallbackPrimary)

	if d.fallbackAgent == nil || d.fallbackAgent.ID() != "primary-fallback" {
		t.Fatalf("SetFallbackAgent did not attach the agent; got %v", d.fallbackAgent)
	}
}

// TestRun_UnifiedMode_DepthExceeded_UsesPrimaryFallback guards the M5 G3
// wiring at the Run level. The dispatcher's depth guard fires before the
// Primary dispatch branch — so whatever SetFallbackAgent attached becomes
// the actual entry point when a nested Dispatcher call recurses back to
// maxRecursionDepth. In unified mode that fallback is the Primary (or a
// degraded variant of it), never the classical chat agent.
func TestRun_UnifiedMode_DepthExceeded_UsesPrimaryFallback(t *testing.T) {
	reg := newTestRegistry()

	chat := &stubAgent{id: "chat"}
	primary := &stubAgent{id: "primary"}
	fallbackPrimary := &stubAgent{
		id: "primary-fallback",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("fallback primary answer"),
				}, "primary-fallback"),
			},
		},
	}

	d := New(
		reg,
		map[string]agent.Agent{"chat": chat},
		nil, nil, nil,
		WithFallbackAgent(chat), // pre-M5 wiring (will be overwritten below)
		WithPrimaryAssistant(primary),
		WithMaxRecursionDepth(1),
	)

	d.SetFallbackAgent(fallbackPrimary) // M5 G3 override

	// Inject depth=maxRecursionDepth so the early-return in Run triggers
	// fallback instead of primary.
	ctx := IncrementDepth(context.Background())

	req := &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("nested")}}
	resp, err := d.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if fallbackPrimary.ranCount() != 1 {
		t.Errorf("fallback primary.ranCount = %d, want 1", fallbackPrimary.ranCount())
	}

	if chat.ranCount() != 0 {
		t.Errorf("chat.ranCount = %d, want 0 (M5 must not fall back to chat in unified mode)", chat.ranCount())
	}

	if primary.ranCount() != 0 {
		t.Errorf("primary.ranCount = %d, want 0 (depth guard must short-circuit before primary branch)", primary.ranCount())
	}

	if len(resp.Messages) == 0 || resp.Messages[0].Content.Text() != "fallback primary answer" {
		t.Errorf("response = %+v, want fallback primary answer", resp.Messages)
	}
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
