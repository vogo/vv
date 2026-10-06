package skillevolve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vogo/vage/skill"
)

func TestWriteSkillMD_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := ExtractedSkill{
		Name:          "from-session",
		Description:   "A reusable workflow",
		Instructions:  "Do the generic thing.",
		ObservedTools: []string{"grep", "read"},
		Metadata: map[string]string{
			"origin":         "evolution",
			"source_session": "sess-1",
			"observed_tools": "grep,read",
		},
	}
	base, created, err := WriteSkillMD(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first write should create the skill directory")
	}
	def, err := (&skill.FileLoader{}).Load(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != s.Name || def.Description != s.Description {
		t.Errorf("loaded %+v", def)
	}
	if !strings.Contains(def.Instructions, "generic") {
		t.Errorf("instructions = %q", def.Instructions)
	}
	if len(def.AllowedTools) != 0 {
		t.Errorf("AllowedTools leaked: %v", def.AllowedTools)
	}
	raw, err := os.ReadFile(filepath.Join(base, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "allowed_tools") {
		t.Errorf("frontmatter contains allowed_tools:\n%s", raw)
	}
	info, err := os.Stat(filepath.Join(base, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != skillFilePerm {
		t.Logf("SKILL.md mode = %o (umask may differ from 0600)", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(base)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != skillDirPerm {
		t.Logf("skill dir mode = %o (umask may differ from 0700)", dirInfo.Mode().Perm())
	}

	_, created2, err := WriteSkillMD(dir, s)
	if err == nil {
		t.Fatal("expected skill exists")
	}
	if created2 {
		t.Fatal("existing skill must not report created")
	}
	var exists *ErrSkillExists
	if !errors.As(err, &exists) {
		t.Fatalf("err = %v, want ErrSkillExists", err)
	}
}

func TestWriteSkillMD_ExistingDirNotRemoved(t *testing.T) {
	dir := t.TempDir()
	s := ExtractedSkill{Name: "keep-me", Description: "d", Instructions: "do it"}
	base := filepath.Join(dir, s.Name)
	if err := os.Mkdir(base, skillDirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "SKILL.md"), []byte("---\nname: keep-me\ndescription: d\n---\n\nold\n"), skillFilePerm); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(base, "KEEPME")
	if err := os.WriteFile(marker, []byte("x"), skillFilePerm); err != nil {
		t.Fatal(err)
	}

	_, created, err := WriteSkillMD(dir, s)
	if err == nil {
		t.Fatal("expected skill exists")
	}
	if created {
		t.Fatal("must not claim created for someone else's directory")
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("someone else's dir was removed: %v", statErr)
	}
	raw, err := os.ReadFile(filepath.Join(base, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "old") {
		t.Fatalf("existing SKILL.md overwritten: %s", raw)
	}
}
