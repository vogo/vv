package skillevolve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/checkpoint"
	"github.com/vogo/vv/registries"
	"github.com/vogo/vv/sessionlogs"
)

type stubRefresh struct{ n int }

func (s *stubRefresh) Refresh() error { s.n++; return nil }

func eligibleFixture(t *testing.T, logs *sessionlogs.Store, sid string) {
	t.Helper()
	msgs := []schema.Message{schema.NewUserMessage(proto, "UNIQUESECRET99 please")}
	msgs = append(msgs, toolTurn("c1", "grep", `{"q":"x"}`, "ok", false)...)
	msgs = append(msgs, schema.NewTextMessage(proto, schema.RoleAssistant, "done"))
	saveCP(t, logs, &checkpoint.Checkpoint{
		SessionID: sid, AgentID: sessionlogs.DefaultPrimaryAgentID,
		Final: true, StopReason: schema.StopReasonComplete, Messages: msgs,
	})
}

func testEngine(t *testing.T, ex Extractor) (*Engine, *registries.SkillStack, string) {
	t.Helper()
	logs := newLogs(t)
	skillDir := t.TempDir()
	stack := registries.LoadSkillStack(context.Background(), "", nil)
	ref := &stubRefresh{}
	eng := &Engine{
		Logs:      logs,
		Extractor: ex,
		Deduper:   &LexicalDeduper{Threshold: 0.85},
		Queue:     NewFileQueue(skillDir),
		Registrar: &Registrar{
			SkillDir:  skillDir,
			VV:        stack.Registry,
			Vage:      stack.VageRegistry,
			Refresher: ref,
		},
		MinTurns:   1,
		MinSuccess: 0.9,
		Vage:       stack.VageRegistry,
	}
	return eng, stack, skillDir
}

func TestEngine_ExtractApproveRoundTrip(t *testing.T) {
	ex := ExtractorFunc(func(_ context.Context, feat SessionFeatures) (ExtractedSkill, error) {
		return ExtractedSkill{
			Name: "session-flow", Description: "reusable flow",
			Instructions: "Do the generic procedure.",
		}, nil
	})
	eng, stack, skillDir := testEngine(t, ex)
	eligibleFixture(t, eng.Logs, "sess-ok")

	p, err := eng.Extract(context.Background(), "sess-ok")
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != StatusPending {
		t.Fatalf("status = %s", p.Status)
	}
	if _, err := os.Stat(filepath.Join(skillDir, ".proposals", p.ID+".json")); err != nil {
		t.Fatal(err)
	}

	approved, err := eng.Approve(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != StatusApproved {
		t.Fatalf("status = %s", approved.Status)
	}
	if !stack.Registry.ValidateRef("session-flow") {
		t.Fatal("vv registry missing skill")
	}
	if _, ok := stack.VageRegistry.Get("session-flow"); !ok {
		t.Fatal("vage registry missing skill")
	}

	p2, err := eng.Extract(context.Background(), "sess-ok")
	if err != nil {
		t.Fatal(err)
	}
	if p2.Status != StatusDuplicate {
		t.Fatalf("second extract status = %s, want duplicate", p2.Status)
	}
	if _, err := eng.Approve(context.Background(), p2.ID); err == nil {
		t.Fatal("duplicate must not approve")
	}
}

func TestEngine_ExtractInterrupted(t *testing.T) {
	ex := ExtractorFunc(func(context.Context, SessionFeatures) (ExtractedSkill, error) {
		return ExtractedSkill{Name: "x"}, nil
	})
	eng, _, _ := testEngine(t, ex)
	saveCP(t, eng.Logs, &checkpoint.Checkpoint{
		SessionID: "sess-int", AgentID: sessionlogs.DefaultPrimaryAgentID,
		Final: true, StopReason: schema.StopReasonInterrupted,
		Messages: []schema.Message{schema.NewUserMessage(proto, "x")},
	})
	_, err := eng.Extract(context.Background(), "sess-int")
	var ne *ErrNotEligible
	if !errors.As(err, &ne) || ne.Eligibility.Reason != ReasonStopReason {
		t.Fatalf("err = %v", err)
	}
}

func TestEngine_Reject(t *testing.T) {
	ex := ExtractorFunc(func(context.Context, SessionFeatures) (ExtractedSkill, error) {
		return ExtractedSkill{Name: "x", Description: "d", Instructions: "i"}, nil
	})
	eng, _, _ := testEngine(t, ex)
	eligibleFixture(t, eng.Logs, "sess-rej")
	p, err := eng.Extract(context.Background(), "sess-rej")
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Reject(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusRejected {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestEngine_ConcurrentApproveSameName(t *testing.T) {
	ex := ExtractorFunc(func(context.Context, SessionFeatures) (ExtractedSkill, error) {
		return ExtractedSkill{
			Name: "session-flow", Description: "reusable flow",
			Instructions: "Do the generic procedure.",
		}, nil
	})
	eng, stack, skillDir := testEngine(t, ex)
	eligibleFixture(t, eng.Logs, "sess-race")

	p1, err := eng.Extract(context.Background(), "sess-race")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := eng.Extract(context.Background(), "sess-race")
	if err != nil {
		t.Fatal(err)
	}
	if p1.Status != StatusPending || p2.Status != StatusPending {
		t.Fatalf("want two pending proposals, got %s and %s", p1.Status, p2.Status)
	}

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := eng.Approve(context.Background(), p1.ID)
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, err := eng.Approve(context.Background(), p2.ID)
		errs <- err
	}()
	wg.Wait()
	close(errs)

	var nOK, nErr int
	for err := range errs {
		if err == nil {
			nOK++
		} else {
			nErr++
		}
	}
	if nOK != 1 || nErr != 1 {
		t.Fatalf("concurrent Approve: successes=%d errors=%d, want 1 and 1", nOK, nErr)
	}

	skillFile := filepath.Join(skillDir, "session-flow", "SKILL.md")
	if _, err := os.Stat(skillFile); err != nil {
		t.Fatalf("winner SKILL.md missing: %v", err)
	}
	if !stack.Registry.ValidateRef("session-flow") {
		t.Fatal("vv registry missing session-flow")
	}
	if _, ok := stack.VageRegistry.Get("session-flow"); !ok {
		t.Fatal("vage registry missing session-flow")
	}
}
