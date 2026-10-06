package cli

import (
	"context"
	"strings"
	"testing"
)

func TestIsSkillCommand(t *testing.T) {
	for _, c := range []string{"/skill-extract", "/skill-proposals", "/skill-approve", "/skill-reject"} {
		if !isSkillCommand(c) {
			t.Errorf("%s should be a skill command", c)
		}
	}
	if isSkillCommand("/memory") || isSkillCommand("/skill") {
		t.Fatal("unexpected match")
	}
}

func TestHandleSkillExtract_NilEngine(t *testing.T) {
	a := &App{}
	text, err := a.handleSkillExtract(context.Background(), "")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(text, "not configured") {
		t.Fatalf("text = %s", text)
	}
	text, err = a.runSkillCommand(context.Background(), []string{"/skill-extract"})
	if err != nil {
		t.Fatalf("runSkillCommand err = %v", err)
	}
	if !strings.Contains(text, "not configured") {
		t.Fatalf("runSkillCommand text=%s", text)
	}
}

func TestHandleSkillList_NilEngineIntercepts(t *testing.T) {
	a := &App{}
	text, err := a.handleSkillList(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(text, "not configured") {
		t.Fatalf("text = %s", text)
	}
}
