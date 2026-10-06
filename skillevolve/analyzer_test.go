package skillevolve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/checkpoint"
	"github.com/vogo/vv/sessionlogs"
)

const proto = schema.ProtocolOpenAIChat

func newLogs(t *testing.T) *sessionlogs.Store {
	t.Helper()
	s, err := sessionlogs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func saveCP(t *testing.T, s *sessionlogs.Store, cp *checkpoint.Checkpoint) {
	t.Helper()
	if err := s.Save(context.Background(), cp); err != nil {
		t.Fatal(err)
	}
}

func toolTurn(callID, name, args, result string, isErr bool) []schema.Message {
	return []schema.Message{
		schema.NewAssistantTurn(proto, "", "", []schema.ToolCall{{ID: callID, Name: name, Arguments: args}}),
		schema.NewToolResultMessage(proto, callID, result, isErr),
	}
}

func TestAnalyze_InvalidID(t *testing.T) {
	_, err := Analyze(context.Background(), newLogs(t), "???")
	if !errors.Is(err, checkpoint.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestAnalyze_EmptySession(t *testing.T) {
	_, err := Analyze(context.Background(), newLogs(t), "sess-empty")
	if !errors.Is(err, checkpoint.ErrCheckpointNotFound) {
		t.Fatalf("err = %v, want ErrCheckpointNotFound", err)
	}
}

func TestAnalyze_TurnsAndEligibility(t *testing.T) {
	s := newLogs(t)
	sid := "sess-turns"
	msgs := []schema.Message{schema.NewUserMessage(proto, "do the thing")}
	msgs = append(msgs, toolTurn("c1", "grep", `{"q":"x"}`, "ok", false)...)
	msgs = append(msgs, toolTurn("c2", "read", `{"path":"a"}`, "ok", false)...)

	for i := 0; i < 3; i++ {
		saveCP(t, s, &checkpoint.Checkpoint{
			SessionID: sid, AgentID: sessionlogs.DefaultPrimaryAgentID,
			Iteration: i + 1, Messages: msgs,
		})
	}
	saveCP(t, s, &checkpoint.Checkpoint{
		SessionID: sid, AgentID: sessionlogs.DefaultPrimaryAgentID,
		Iteration: 4, Final: true, StopReason: schema.StopReasonComplete,
		Messages: append(msgs, schema.NewTextMessage(proto, schema.RoleAssistant, "done")),
	})

	feat, err := Analyze(context.Background(), s, sid)
	if err != nil {
		t.Fatal(err)
	}
	if feat.Eligibility.Turns != 4 {
		t.Errorf("Turns = %d, want 4", feat.Eligibility.Turns)
	}
	if feat.Eligibility.ToolCalls != 2 {
		t.Errorf("ToolCalls = %d, want 2", feat.Eligibility.ToolCalls)
	}
	if feat.Eligibility.SuccessRate != 1 {
		t.Errorf("SuccessRate = %v, want 1", feat.Eligibility.SuccessRate)
	}

	el := Eligible(feat, 5, 0.9)
	if el.OK || el.Reason != ReasonTooFewTurns {
		t.Errorf("minTurns=5: %+v", el)
	}
	el = Eligible(feat, 3, 0.9)
	if !el.OK {
		t.Errorf("minTurns=3 should pass, got %+v", el)
	}
}

func TestAnalyze_TwoRoundCumulativeToolCalls(t *testing.T) {
	s := newLogs(t)
	sid := "sess-tworound"

	round1 := []schema.Message{schema.NewUserMessage(proto, "first")}
	round1 = append(round1, toolTurn("c1", "grep", `{"q":"a"}`, "hit", false)...)
	round1 = append(round1, schema.NewTextMessage(proto, schema.RoleAssistant, "ok1"))
	saveCP(t, s, &checkpoint.Checkpoint{
		SessionID: sid, AgentID: sessionlogs.DefaultPrimaryAgentID,
		Iteration: 1, Final: true, StopReason: schema.StopReasonComplete,
		Messages: round1,
	})

	round2 := append(append([]schema.Message{}, round1...), schema.NewUserMessage(proto, "second"))
	round2 = append(round2, toolTurn("c2", "read", `{"path":"f"}`, "body", false)...)
	round2 = append(round2, schema.NewTextMessage(proto, schema.RoleAssistant, "ok2"))
	saveCP(t, s, &checkpoint.Checkpoint{
		SessionID: sid, AgentID: sessionlogs.DefaultPrimaryAgentID,
		Iteration: 1, Final: true, StopReason: schema.StopReasonComplete,
		Messages: round2,
	})

	feat, err := Analyze(context.Background(), s, sid)
	if err != nil {
		t.Fatal(err)
	}
	if feat.Eligibility.ToolCalls != 2 {
		t.Fatalf("latest Messages ToolCalls = %d, want 2 (both rounds)", feat.Eligibility.ToolCalls)
	}
}

func TestAnalyze_AllToolErrors(t *testing.T) {
	s := newLogs(t)
	sid := "sess-errors"
	msgs := []schema.Message{schema.NewUserMessage(proto, "x")}
	msgs = append(msgs, toolTurn("c1", "bash", `{"cmd":"x"}`, "fail", true)...)
	saveCP(t, s, &checkpoint.Checkpoint{
		SessionID: sid, AgentID: sessionlogs.DefaultPrimaryAgentID,
		Final: true, StopReason: schema.StopReasonComplete, Messages: msgs,
	})
	feat, err := Analyze(context.Background(), s, sid)
	if err != nil {
		t.Fatal(err)
	}
	el := Eligible(feat, 1, 0.9)
	if el.OK || el.Reason != ReasonToolSuccessRate {
		t.Errorf("got %+v", el)
	}
	if feat.Eligibility.SuccessRate != 0 {
		t.Errorf("SuccessRate = %v, want 0", feat.Eligibility.SuccessRate)
	}
}

func TestAnalyze_StopReasons(t *testing.T) {
	cases := []schema.StopReason{
		schema.StopReasonInterrupted,
		schema.StopReasonMaxIterations,
		schema.StopReasonBudgetExhausted,
	}
	for _, stop := range cases {
		t.Run(string(stop), func(t *testing.T) {
			s := newLogs(t)
			sid := "sess-" + strings.ReplaceAll(string(stop), "_", "-")
			saveCP(t, s, &checkpoint.Checkpoint{
				SessionID: sid, AgentID: sessionlogs.DefaultPrimaryAgentID,
				Final: true, StopReason: stop,
				Messages: []schema.Message{schema.NewUserMessage(proto, "x")},
			})
			feat, err := Analyze(context.Background(), s, sid)
			if err != nil {
				t.Fatal(err)
			}
			el := Eligible(feat, 1, 0.9)
			if el.OK || el.Reason != ReasonStopReason {
				t.Errorf("stop %s: %+v", stop, el)
			}
		})
	}
}

func TestAnalyze_NoToolCalls(t *testing.T) {
	s := newLogs(t)
	sid := "sess-notools"
	saveCP(t, s, &checkpoint.Checkpoint{
		SessionID: sid, AgentID: sessionlogs.DefaultPrimaryAgentID,
		Final: true, StopReason: schema.StopReasonComplete,
		Messages: []schema.Message{
			schema.NewUserMessage(proto, "hi"),
			schema.NewTextMessage(proto, schema.RoleAssistant, "hello"),
		},
	})
	feat, err := Analyze(context.Background(), s, sid)
	if err != nil {
		t.Fatal(err)
	}
	el := Eligible(feat, 1, 0.9)
	if el.OK || el.Reason != ReasonNoToolCalls {
		t.Errorf("got %+v", el)
	}
}

func TestAnalyze_SubagentToolsDoNotCountForGate(t *testing.T) {
	s := newLogs(t)
	sid := "sess-sub"
	saveCP(t, s, &checkpoint.Checkpoint{
		SessionID: sid, AgentID: sessionlogs.DefaultPrimaryAgentID,
		Final: true, StopReason: schema.StopReasonComplete,
		Messages: []schema.Message{schema.NewUserMessage(proto, "delegate")},
	})
	runCtx := sessionlogs.WithRun(context.Background(), sessionlogs.Run{Key: "d1", Task: "work"})
	subMsgs := []schema.Message{schema.NewUserMessage(proto, "sub")}
	subMsgs = append(subMsgs, toolTurn("c1", "read", `{}`, "ok", false)...)
	if err := s.Save(runCtx, &checkpoint.Checkpoint{
		SessionID: sid, AgentID: "coder",
		Final: true, StopReason: schema.StopReasonComplete, Messages: subMsgs,
	}); err != nil {
		t.Fatal(err)
	}

	feat, err := Analyze(context.Background(), s, sid)
	if err != nil {
		t.Fatal(err)
	}
	el := Eligible(feat, 1, 0.9)
	if el.OK || el.Reason != ReasonNoToolCalls {
		t.Errorf("main chain should be no_tool_calls, got %+v", el)
	}
	found := false
	for _, step := range feat.ToolSequence {
		if step.Name == "read" && step.AgentID == "coder" {
			found = true
		}
	}
	if !found {
		t.Errorf("ToolSequence missing sub-agent step: %+v", feat.ToolSequence)
	}
}

func TestAnalyze_NotFinalComplete(t *testing.T) {
	s := newLogs(t)
	sid := "sess-not-final"
	msgs := []schema.Message{schema.NewUserMessage(proto, "x")}
	msgs = append(msgs, toolTurn("c1", "grep", `{"q":"x"}`, "ok", false)...)
	saveCP(t, s, &checkpoint.Checkpoint{
		SessionID: sid, AgentID: sessionlogs.DefaultPrimaryAgentID,
		Final: false, StopReason: schema.StopReasonComplete, Messages: msgs,
	})
	feat, err := Analyze(context.Background(), s, sid)
	if err != nil {
		t.Fatal(err)
	}
	if feat.Eligibility.Final {
		t.Fatal("Analyze should copy Final=false from latest checkpoint")
	}
	el := Eligible(feat, 1, 0.9)
	if el.OK || el.Reason != ReasonNotFinal {
		t.Errorf("Final=false StopReason=complete: %+v", el)
	}
}

func TestAnalyze_UserTaskTruncated(t *testing.T) {
	s := newLogs(t)
	sid := "sess-long"
	long := strings.Repeat("x", 3000)
	saveCP(t, s, &checkpoint.Checkpoint{
		SessionID: sid, AgentID: sessionlogs.DefaultPrimaryAgentID,
		Final: true, StopReason: schema.StopReasonComplete,
		Messages: []schema.Message{schema.NewUserMessage(proto, long)},
	})
	feat, err := Analyze(context.Background(), s, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(feat.UserTask) > 2048 {
		t.Errorf("UserTask len = %d, want <= 2048", len(feat.UserTask))
	}
}
