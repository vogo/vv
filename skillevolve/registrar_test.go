package skillevolve

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vogo/vage/skill"
	"github.com/vogo/vv/registries"
)

func TestRegistrar_RegisterAndRefresh(t *testing.T) {
	dir := t.TempDir()
	stack := registries.LoadSkillStack(context.Background(), "", nil)
	ref := &stubRefresh{}
	r := &Registrar{
		SkillDir:  dir,
		VV:        stack.Registry,
		Vage:      stack.VageRegistry,
		Refresher: ref,
	}
	if _, err := r.Register(context.Background(), ExtractedSkill{
		Name: "reg-skill", Description: "d", Instructions: "do it",
		Metadata: map[string]string{"origin": "evolution"},
	}); err != nil {
		t.Fatal(err)
	}
	if ref.n != 1 {
		t.Errorf("Refresh called %d times", ref.n)
	}
	def, err := (&skill.FileLoader{}).Load(context.Background(), dir+"/reg-skill")
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "reg-skill" {
		t.Errorf("name = %s", def.Name)
	}
}

func TestRegistrar_DoesNotRemoveExistingSkillDir(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "keep-skill")
	if err := os.Mkdir(base, skillDirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "SKILL.md"), []byte("---\nname: keep-skill\ndescription: d\n---\n\nbody\n"), skillFilePerm); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(base, "KEEPME")
	if err := os.WriteFile(marker, []byte("x"), skillFilePerm); err != nil {
		t.Fatal(err)
	}

	stack := registries.LoadSkillStack(context.Background(), "", nil)
	r := &Registrar{SkillDir: dir, VV: stack.Registry, Vage: stack.VageRegistry}
	if _, err := r.Register(context.Background(), ExtractedSkill{
		Name: "keep-skill", Description: "d", Instructions: "do it",
	}); err == nil {
		t.Fatal("expected skill exists")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("registrar removed someone else's directory: %v", err)
	}
	if stack.Registry.ValidateRef("keep-skill") {
		t.Fatal("must not register after refusing to overwrite existing dir")
	}
}
