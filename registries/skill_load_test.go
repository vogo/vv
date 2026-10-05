package registries

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vogo/largemodel/schema"
)

func TestDefaultSkillsWithFileSkills_EmptyDirUnchanged(t *testing.T) {
	reg, err := DefaultSkillsWithFileSkills(context.Background(), "")
	if err != nil {
		t.Fatalf("DefaultSkillsWithFileSkills: %v", err)
	}

	got := reg.IDs()
	if len(got) != 2 || got[0] != SkillResearch || got[1] != SkillReview {
		t.Errorf("IDs() = %v, want built-ins only", got)
	}
}

func TestLoadSkillStack_MergesValidSkipsInvalid(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "security-audit", "Security review discipline", "Look for auth holes.", "")
	writeSkillMD(t, dir, "release-notes", "Release note voice", "Write a changelog.", "allowed_tools:\n  - bash\n")
	writeSkillMD(t, dir, "Not-Valid", "bad name", "nope", "") // fails NameValidator
	writeSkillMD(t, dir, SkillReview, "conflict", "should skip", "")

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	stack := LoadSkillStack(context.Background(), dir, nil)
	ids := stack.Registry.IDs()

	if !containsID(ids, "security-audit") || !containsID(ids, "release-notes") {
		t.Errorf("IDs() = %v, want file skills merged", ids)
	}
	if containsID(ids, "Not-Valid") {
		t.Errorf("invalid skill leaked into registry: %v", ids)
	}
	if got, ok := stack.Registry.Get(SkillReview); !ok || !strings.Contains(got.Instructions, "评审意见") {
		t.Error("built-in review skill was overwritten by the conflicting file")
	}

	logs := buf.String()
	if !strings.Contains(logs, "allowed_tools") {
		t.Errorf("expected Warn mentioning allowed_tools, got %q", logs)
	}
	if !strings.Contains(logs, "conflicts") && !strings.Contains(logs, "skip file skill") {
		t.Errorf("expected skip/conflict Warn, got %q", logs)
	}

	if _, err := stack.Manager.Activate(context.Background(), "security-audit", "s1"); err != nil {
		t.Fatalf("Activate file skill: %v", err)
	}
	act := stack.Manager.ActiveSkills("s1")
	if len(act) != 1 {
		t.Fatalf("ActiveSkills = %d, want 1", len(act))
	}
	if tools := act[0].SkillDef().AllowedTools; len(tools) != 0 {
		t.Errorf("AllowedTools leaked into manager: %v", tools)
	}
}

func TestLoadSkillStack_MissingDirDoesNotFail(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	stack := LoadSkillStack(context.Background(), filepath.Join(t.TempDir(), "no-such"), nil)
	if got := stack.Registry.IDs(); len(got) != 2 {
		t.Errorf("missing dir should keep built-ins, got %v", got)
	}
	if !strings.Contains(buf.String(), "discover failed") {
		t.Errorf("expected discover-failed Warn, got %q", buf.String())
	}
}

func TestAdaptSkillEvents_FillsEnvelopeSessionID(t *testing.T) {
	var got schema.Event
	adapted := adaptSkillEvents(func(_ context.Context, e schema.Event) { got = e })
	adapted(context.Background(), schema.NewEvent(schema.EventSkillActivate, "", "", schema.SkillActivateData{
		SkillName: "review",
		SessionID: "sess-9",
	}))
	if got.SessionID != "sess-9" {
		t.Errorf("SessionID = %q, want sess-9", got.SessionID)
	}

	if adaptSkillEvents(nil) != nil {
		t.Error("nil dispatcher should stay nil")
	}
}

func writeSkillMD(t *testing.T, root, name, description, body, extra string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n" + extra + "---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
