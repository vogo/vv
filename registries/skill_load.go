package registries

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/skill"
)

// SkillStack is the startup-time pair consumed by setup: the vv registry
// (spawn_worker / DAG) and the vage Manager (Primary use_skill / next-run
// prompt injection). VageRegistry is the same pointer the Manager holds,
// so runtime RegisterFileSkill is visible to Activate.
type SkillStack struct {
	Registry     *SkillRegistry
	Manager      skill.Manager
	VageRegistry skill.Registry
}

// DefaultSkillsWithFileSkills returns the built-in registry merged with
// SKILL.md files discovered under dir. An empty dir is equivalent to
// DefaultSkills. Discovery / validation / ID-conflict failures are logged
// and skipped; the function never fails startup.
func DefaultSkillsWithFileSkills(ctx context.Context, dir string) (*SkillRegistry, error) {
	return LoadSkillStack(ctx, dir, nil).Registry, nil
}

// LoadSkillStack constructs the built-in skills, optionally discovers file
// skills from dir, and returns a Manager whose registry is a parallel view
// of the same IDs with AllowedTools stripped (AGENTS-R11).
func LoadSkillStack(ctx context.Context, dir string, dispatch skill.EventDispatcher) *SkillStack {
	vvReg := DefaultSkills()
	vageReg := skill.NewRegistry(skill.WithValidator(skill.DefaultValidator()))

	for _, s := range vvReg.All() {
		if err := vageReg.Register(skillToDef(s)); err != nil {
			slog.Warn("vv: skip built-in skill in vage registry", "skill", s.ID, "error", err)
		}
	}

	if trimmed := strings.TrimSpace(dir); trimmed != "" {
		mergeFileSkills(ctx, vvReg, vageReg, trimmed)
	}

	mgr := skill.NewManager(vageReg, skill.WithEventDispatcher(adaptSkillEvents(dispatch)))

	return &SkillStack{Registry: vvReg, Manager: mgr, VageRegistry: vageReg}
}

func mergeFileSkills(ctx context.Context, vvReg *SkillRegistry, vageReg skill.Registry, dir string) {
	defs, err := (&skill.FileLoader{}).Discover(ctx, dir)
	if err != nil {
		slog.Warn("vv: skill discover failed", "dir", dir, "error", err)
		return
	}

	added := 0
	for _, def := range defs {
		if def == nil {
			continue
		}
		if mergeOneFileSkill(vvReg, vageReg, def) {
			added++
		}
	}

	slog.Info("vv: file skills loaded", "dir", dir, "added", added, "discovered", len(defs))
}

func mergeOneFileSkill(vvReg *SkillRegistry, vageReg skill.Registry, def *skill.Def) bool {
	if err := RegisterFileSkill(vvReg, vageReg, def); err != nil {
		slog.Warn("vv: skip file skill", "skill", def.Name, "error", err)
		return false
	}
	return true
}

// vvSkillRegistry is the vv-side surface RegisterFileSkill uses.
// *SkillRegistry implements it; tests may substitute a failing wrapper so
// vage Unregister after a vv Register failure is actually exercised.
type vvSkillRegistry interface {
	ValidateRef(id string) bool
	Register(s Skill) error
}

// RegisterFileSkill copies def, strips AllowedTools (AGENTS-R11), and
// registers into both vv and vage registries. The caller's def is not
// mutated. If vv Register fails after a successful vage Register, vage is
// Unregister'd so the two views stay aligned.
func RegisterFileSkill(vvReg vvSkillRegistry, vageReg skill.Registry, def *skill.Def) error {
	if def == nil {
		return fmt.Errorf("skill definition is nil")
	}
	if vvReg == nil || vageReg == nil {
		return fmt.Errorf("skill registries are required")
	}

	if len(def.AllowedTools) > 0 {
		slog.Warn("vv: skill allowed_tools not applied this release; use tool_access to express the tool subset",
			"skill", def.Name, "allowed_tools", def.AllowedTools)
	}

	if vvReg.ValidateRef(def.Name) {
		return fmt.Errorf("id conflicts with a built-in or already-registered skill")
	}

	stripped := *def
	stripped.AllowedTools = nil

	if err := vageReg.Register(&stripped); err != nil {
		return err
	}

	if err := vvReg.Register(Skill{
		ID:           def.Name,
		Description:  def.Description,
		Instructions: def.Instructions,
	}); err != nil {
		vageReg.Unregister(def.Name)
		return err
	}

	return nil
}

func skillToDef(s Skill) *skill.Def {
	return &skill.Def{
		Name:         s.ID,
		Description:  s.Description,
		Instructions: s.Instructions,
	}
}

// adaptSkillEvents copies session_id from skill event payloads onto the
// envelope so session / metrics / trace hooks (which skip empty SessionID)
// can observe activate / deactivate.
func adaptSkillEvents(d skill.EventDispatcher) skill.EventDispatcher {
	if d == nil {
		return nil
	}

	return func(ctx context.Context, event schema.Event) {
		if event.SessionID == "" {
			switch data := event.Data.(type) {
			case schema.SkillActivateData:
				event.SessionID = data.SessionID
			case schema.SkillDeactivateData:
				event.SessionID = data.SessionID
			}
		}
		d(ctx, event)
	}
}
