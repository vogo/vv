package dispatches

import (
	"testing"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/tool/bash"
)

func TestDangerousBashPolicy_OnlyDangerousBash(t *testing.T) {
	c := bash.NewClassifier(bash.DefaultRules())
	p := NewDangerousBashPolicy(c, nil, "fp")

	calls := []schema.ToolCall{
		{ID: "safe", Name: "bash", Arguments: `{"command":"ls"}`},
		{ID: "danger", Name: "bash", Arguments: `{"command":"rm -rf ./dist"}`},
		{ID: "blocked", Name: "bash", Arguments: `{"command":"sudo rm -rf /"}`},
		{ID: "write", Name: "write", Arguments: `{"path":"a.go","content":"x"}`},
		{ID: "malformed", Name: "bash", Arguments: `not-json`},
	}

	got := p.Intercept(t.Context(), "sess", calls)
	if len(got) != 1 || got[0] != "danger" {
		t.Fatalf("Intercept = %v, want [danger]", got)
	}
}

func TestDangerousBashPolicy_NilClassifierNeverFlags(t *testing.T) {
	p := NewDangerousBashPolicy(nil, nil, "fp")
	got := p.Intercept(t.Context(), "sess", []schema.ToolCall{
		{ID: "d", Name: "bash", Arguments: `{"command":"rm -rf ./dist"}`},
	})
	if len(got) != 0 {
		t.Fatalf("Intercept = %v, want empty", got)
	}
}

func TestDangerousBashPolicy_EmptyBatch(t *testing.T) {
	p := NewDangerousBashPolicy(bash.NewClassifier(bash.DefaultRules()), nil, "fp")
	if got := p.Intercept(t.Context(), "sess", nil); len(got) != 0 {
		t.Fatalf("Intercept(nil) = %v, want empty", got)
	}
}

func TestDangerousBashPolicy_WitnessMatchesIntercept(t *testing.T) {
	c := bash.NewClassifier(bash.DefaultRules())
	p := NewDangerousBashPolicy(c, nil, "fp-test")
	w, ok := p.(taskagent.InterruptWitness)
	if !ok {
		t.Fatal("policy must implement InterruptWitness")
	}
	calls := []schema.ToolCall{
		{ID: "safe", Name: "bash", Arguments: `{"command":"ls"}`},
		{ID: "danger", Name: "bash", Arguments: `{"command":"rm -rf ./dist"}`},
		{ID: "blocked", Name: "bash", Arguments: `{"command":"sudo rm -rf /"}`},
	}
	pending := p.Intercept(t.Context(), "sess", calls)
	snap := w.Witness(t.Context(), "sess", calls)
	if snap.Fingerprint != "fp-test" {
		t.Fatalf("fingerprint = %q", snap.Fingerprint)
	}
	if len(snap.Calls) != len(calls) {
		t.Fatalf("assessments = %d, want %d", len(snap.Calls), len(calls))
	}
	flagged := map[string]struct{}{}
	for _, id := range pending {
		flagged[id] = struct{}{}
	}
	for _, assessment := range snap.Calls {
		_, want := flagged[assessment.ToolCallID]
		if assessment.Flagged != want {
			t.Errorf("assessment %s flagged=%v, intercept=%v", assessment.ToolCallID, assessment.Flagged, want)
		}
		if assessment.Flagged && assessment.Classification == "" {
			t.Errorf("flagged %s missing classification", assessment.ToolCallID)
		}
	}
}
