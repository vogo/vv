package dispatches

import (
	"context"
	"strings"
	"testing"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

// maxIterStreamAgent emits one tool call and then ends the stream with
// StopReasonMaxIterations and no assistant text — the exact shape of a run
// that exhausts its iteration ceiling mid-investigation.
type maxIterStreamAgent struct {
	stubAgent

	stop schema.StopReason
}

var _ agent.StreamAgent = (*maxIterStreamAgent)(nil)

func (s *maxIterStreamAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, agent.DefaultStreamBufferSize, func(_ context.Context, send func(schema.Event) error) error {
		if err := send(schema.NewEvent(schema.EventToolCallStart, s.id, req.SessionID, schema.ToolCallStartData{
			ToolName:  "read",
			Arguments: `{"file_path":"/repo/doc/overview.md"}`,
		})); err != nil {
			return err
		}

		return send(schema.NewEvent(schema.EventAgentEnd, s.id, req.SessionID, schema.AgentEndData{
			StopReason: s.stop,
		}))
	}), nil
}

func TestIsIncompleteStop(t *testing.T) {
	t.Parallel()

	cases := []struct {
		stop schema.StopReason
		want bool
	}{
		{schema.StopReasonMaxIterations, true},
		{schema.StopReasonBudgetExhausted, true},
		{schema.StopReasonComplete, false},
		{"", false},
	}

	for _, tc := range cases {
		if got := isIncompleteStop(tc.stop); got != tc.want {
			t.Errorf("isIncompleteStop(%q) = %v, want %v", tc.stop, got, tc.want)
		}
	}
}

// TestRunPrimaryStream_FinalizesOnMaxIterations is the regression test for the
// silent-failure mode: a Primary that burns its iteration budget used to end
// the turn with tool noise and no reply. The dispatcher must now relay the
// original events untouched and then append a salvaged closing answer.
func TestRunPrimaryStream_FinalizesOnMaxIterations(t *testing.T) {
	primary := &maxIterStreamAgent{
		stubAgent: stubAgent{id: "primary"},
		stop:      schema.StopReasonMaxIterations,
	}

	finalizer := &stubAgent{
		id: "fallback",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleAssistant, "here is what I found so far"),
			},
		},
	}

	d := New(
		newTestRegistry(), nil, nil,
		WithPrimaryAssistant(primary),
		WithFallbackAgent(finalizer),
	)

	var (
		texts []string
		ends  []schema.AgentEndData
	)

	send := func(ev schema.Event) error {
		switch ev.Type {
		case schema.EventTextDelta:
			if data, ok := ev.Data.(schema.TextDeltaData); ok {
				texts = append(texts, data.Delta)
			}
		case schema.EventAgentEnd:
			if data, ok := ev.Data.(schema.AgentEndData); ok {
				ends = append(ends, data)
			}
		}

		return nil
	}

	req := &schema.RunRequest{Messages: []schema.Message{
		schema.NewUserMessage(schema.ProtocolOpenAIChat, "create readme to intro the usage of vv"),
	}}

	if err := d.runPrimaryStream(context.Background(), send, req); err != nil {
		t.Fatalf("runPrimaryStream: %v", err)
	}

	if finalizer.ranCount() != 1 {
		t.Fatalf("finalizer.ranCount = %d, want 1", finalizer.ranCount())
	}

	joined := strings.Join(texts, "")
	if !strings.Contains(joined, "here is what I found so far") {
		t.Errorf("relayed text = %q, want the salvaged answer", joined)
	}

	if len(ends) != 2 {
		t.Fatalf("AgentEnd events = %d, want 2 (original + salvage)", len(ends))
	}

	if ends[len(ends)-1].StopReason != schema.StopReasonMaxIterations {
		t.Errorf("final stop reason = %q, want it preserved as %q",
			ends[len(ends)-1].StopReason, schema.StopReasonMaxIterations)
	}
}

// TestRunPrimaryStream_NoFinalizeOnComplete keeps the salvage path off the
// happy road: a normal run must not pay for an extra LLM call.
func TestRunPrimaryStream_NoFinalizeOnComplete(t *testing.T) {
	primary := &maxIterStreamAgent{
		stubAgent: stubAgent{id: "primary"},
		stop:      schema.StopReasonComplete,
	}

	finalizer := &stubAgent{id: "fallback"}

	d := New(
		newTestRegistry(), nil, nil,
		WithPrimaryAssistant(primary),
		WithFallbackAgent(finalizer),
	)

	err := d.runPrimaryStream(context.Background(), func(schema.Event) error { return nil },
		&schema.RunRequest{Messages: []schema.Message{
			schema.NewUserMessage(schema.ProtocolOpenAIChat, "hi"),
		}})
	if err != nil {
		t.Fatalf("runPrimaryStream: %v", err)
	}

	if finalizer.ranCount() != 0 {
		t.Errorf("finalizer.ranCount = %d, want 0", finalizer.ranCount())
	}
}

// TestRunPrimary_FinalizesResponseOnMaxIterations covers the non-streaming
// entry point (HTTP sync mode, MCP).
func TestRunPrimary_FinalizesResponseOnMaxIterations(t *testing.T) {
	primary := &stubAgent{
		id: "primary",
		response: &schema.RunResponse{
			StopReason: schema.StopReasonMaxIterations,
		},
	}

	finalizer := &stubAgent{
		id: "fallback",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleAssistant, "partial findings"),
			},
		},
	}

	d := New(
		newTestRegistry(), nil, nil,
		WithPrimaryAssistant(primary),
		WithFallbackAgent(finalizer),
	)

	resp, err := d.runPrimary(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "do the thing")},
	})
	if err != nil {
		t.Fatalf("runPrimary: %v", err)
	}

	if len(resp.Messages) != 1 || !strings.Contains(resp.Messages[0].Text(), "partial findings") {
		t.Errorf("messages = %+v, want the salvaged closing message appended", resp.Messages)
	}

	if resp.StopReason != schema.StopReasonMaxIterations {
		t.Errorf("StopReason = %q, want it preserved", resp.StopReason)
	}
}

// TestFinalizer_NoFallbackAgent proves the salvage path degrades quietly when
// no tool-free agent is wired: the run keeps whatever it produced.
func TestFinalizer_NoFallbackAgent(t *testing.T) {
	d := New(newTestRegistry(), nil, nil)

	text, err := d.runFinalizer(context.Background(),
		&schema.RunRequest{}, schema.StopReasonMaxIterations, "")
	if err != nil {
		t.Fatalf("runFinalizer: %v", err)
	}

	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}
}

func TestPrimaryRunObserver_RecordsToolTrailAndStop(t *testing.T) {
	t.Parallel()

	obs := &primaryRunObserver{}

	obs.observe(schema.NewEvent(schema.EventToolCallStart, "primary", "s", schema.ToolCallStartData{
		ToolName:  "glob",
		Arguments: strings.Repeat("x", maxFinalizeArgLen+50),
	}))
	obs.observe(schema.NewEvent(schema.EventAgentEnd, "primary", "s", schema.AgentEndData{
		StopReason: schema.StopReasonMaxIterations,
	}))

	if obs.stop != schema.StopReasonMaxIterations {
		t.Errorf("stop = %q, want %q", obs.stop, schema.StopReasonMaxIterations)
	}

	activity := obs.activity()
	if !strings.HasPrefix(activity, "- glob ") {
		t.Errorf("activity = %q, want it to start with the tool name", activity)
	}

	if !strings.Contains(activity, "…") {
		t.Errorf("activity = %q, want oversized arguments truncated", activity)
	}
}

func TestFinalizeInstruction_MentionsReasonAndActivity(t *testing.T) {
	t.Parallel()

	got := finalizeInstruction(schema.StopReasonMaxIterations, "- read /repo/main.go\n")
	if !strings.Contains(got, "tool-iteration budget") {
		t.Errorf("instruction = %q, want the iteration ceiling named", got)
	}

	if !strings.Contains(got, "/repo/main.go") {
		t.Errorf("instruction = %q, want the tool trail included", got)
	}

	budget := finalizeInstruction(schema.StopReasonBudgetExhausted, "")
	if !strings.Contains(budget, "token budget") {
		t.Errorf("instruction = %q, want the token budget named", budget)
	}
}
