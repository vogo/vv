package registries

import (
	"strings"
	"testing"
)

func TestDefaultSkills_RegistersBuiltins(t *testing.T) {
	reg := DefaultSkills()

	for _, id := range []string{SkillReview, SkillResearch} {
		s, ok := reg.Get(id)
		if !ok {
			t.Fatalf("built-in skill %q not registered", id)
		}

		if strings.TrimSpace(s.Instructions) == "" {
			t.Errorf("skill %q has empty instructions", id)
		}

		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("skill %q has empty description", id)
		}
	}

	if got := reg.IDs(); len(got) != 2 || got[0] != SkillResearch || got[1] != SkillReview {
		t.Errorf("IDs() = %v, want sorted [research review]", got)
	}
}

// The review skill must constrain the *output*, never claim tool authority:
// tool access is decided by ToolProfile assembly and guard/sandbox alone.
func TestReviewSkill_ConstrainsOutputNotTools(t *testing.T) {
	if !strings.Contains(ReviewSkillInstructions, "不修改任何文件") {
		t.Error("review skill lost its no-modification instruction")
	}

	for _, forbidden := range []string{"tool_access", "ProfileFull", "write 工具已授予"} {
		if strings.Contains(ReviewSkillInstructions, forbidden) {
			t.Errorf("review skill must not describe tool grants, found %q", forbidden)
		}
	}
}

func TestSkillRegistry_RegisterErrors(t *testing.T) {
	reg := NewSkills()

	if err := reg.Register(Skill{ID: "  "}); err == nil {
		t.Error("expected error for empty skill ID")
	}

	if err := reg.Register(Skill{ID: "x", Instructions: "a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := reg.Register(Skill{ID: "x", Instructions: "b"}); err == nil {
		t.Error("expected duplicate skill ID to be rejected")
	}
}

// Skill ID collisions are startup-time programming errors, mirroring the agent
// registry's MustRegister contract.
func TestSkillRegistry_MustRegisterPanicsOnDuplicate(t *testing.T) {
	reg := NewSkills()
	reg.MustRegister(Skill{ID: "dup"})

	defer func() {
		if recover() == nil {
			t.Error("MustRegister did not panic on duplicate ID")
		}
	}()

	reg.MustRegister(Skill{ID: "dup"})
}

func TestSkillRegistry_Instructions(t *testing.T) {
	reg := NewSkills()
	reg.MustRegister(Skill{ID: "a", Instructions: "AAA"})
	reg.MustRegister(Skill{ID: "b", Instructions: "BBB"})
	reg.MustRegister(Skill{ID: "empty"})

	tests := []struct {
		name    string
		ids     []string
		want    string
		wantErr bool
	}{
		{name: "none", ids: nil, want: ""},
		{name: "single", ids: []string{"a"}, want: "AAA"},
		{name: "ordered concat", ids: []string{"b", "a"}, want: "BBB\n\nAAA"},
		{name: "empty instructions skipped", ids: []string{"a", "empty"}, want: "AAA"},
		{name: "unknown skill", ids: []string{"nope"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := reg.Instructions(tt.ids)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.want {
				t.Errorf("Instructions() = %q, want %q", got, tt.want)
			}
		})
	}
}
