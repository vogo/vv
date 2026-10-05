package dispatches

import (
	"testing"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/tool/bash"
)

func TestDangerousBashPolicy_OnlyDangerousBash(t *testing.T) {
	c := bash.NewClassifier(bash.DefaultRules())
	p := NewDangerousBashPolicy(c, nil)

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
	p := NewDangerousBashPolicy(nil, nil)
	got := p.Intercept(t.Context(), "sess", []schema.ToolCall{
		{ID: "d", Name: "bash", Arguments: `{"command":"rm -rf ./dist"}`},
	})
	if len(got) != 0 {
		t.Fatalf("Intercept = %v, want empty", got)
	}
}

func TestDangerousBashPolicy_EmptyBatch(t *testing.T) {
	p := NewDangerousBashPolicy(bash.NewClassifier(bash.DefaultRules()), nil)
	if got := p.Intercept(t.Context(), "sess", nil); got != nil && len(got) != 0 {
		t.Fatalf("Intercept(nil) = %v, want empty", got)
	}
}
