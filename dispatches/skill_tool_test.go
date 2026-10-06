package dispatches

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/skill"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/registries"
)

func TestRegisterUseSkillTool_RequiresManager(t *testing.T) {
	if _, err := RegisterUseSkillTool(tool.NewRegistry(), registries.DefaultSkills(), nil); err == nil {
		t.Fatal("expected error when manager is nil")
	}
}

func TestRegisterUseSkillTool_SchemaAndActivate(t *testing.T) {
	reg := tool.NewRegistry()
	skills := registries.DefaultSkills()
	stack := registries.LoadSkillStack(context.Background(), "", nil)

	if _, err := RegisterUseSkillTool(reg, skills, stack.Manager); err != nil {
		t.Fatalf("RegisterUseSkillTool: %v", err)
	}

	def, ok := lookupTool(reg, PrimaryToolUseSkill)
	if !ok {
		t.Fatal("use_skill not registered")
	}
	if !strings.Contains(def.Description, "NEXT turn") {
		t.Errorf("description must say next-turn semantics, got %q", def.Description)
	}
	params, _ := def.Parameters.(map[string]any)
	props, _ := params["properties"].(map[string]any)
	skillParam, _ := props["skill"].(map[string]any)
	enum, _ := skillParam["enum"].([]string)
	if len(enum) != 2 {
		t.Errorf("enum = %v, want built-in IDs", enum)
	}

	ctx := schema.WithSessionID(context.Background(), "sess-a")

	res, err := reg.Execute(ctx, PrimaryToolUseSkill, `{"skill":"review"}`)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if res.IsError {
		t.Fatalf("activate error: %s", res.Text())
	}
	if !strings.Contains(res.Text(), "next turn") {
		t.Errorf("result must echo next-turn semantics, got %q", res.Text())
	}

	res2, err := reg.Execute(ctx, PrimaryToolUseSkill, `{"skill":"review"}`)
	if err != nil {
		t.Fatalf("repeat activate: %v", err)
	}
	if res2.IsError {
		t.Fatalf("repeat activate should be idempotent, got %s", res2.Text())
	}
	if !strings.Contains(res2.Text(), "already active") {
		t.Errorf("repeat result = %q, want already-active", res2.Text())
	}

	if n := len(stack.Manager.ActiveSkills("sess-a")); n != 1 {
		t.Errorf("ActiveSkills = %d, want 1 (no duplicate)", n)
	}

	res3, err := reg.Execute(ctx, PrimaryToolUseSkill, `{"skill":"nope"}`)
	if err != nil {
		t.Fatalf("unknown: %v", err)
	}
	if !res3.IsError {
		t.Fatal("unknown skill should be ErrorResult")
	}
	if !strings.Contains(res3.Text(), "research") || !strings.Contains(res3.Text(), "review") {
		t.Errorf("unknown error should list available IDs, got %q", res3.Text())
	}
}

func TestRegisterUseSkillTool_JSONRoundTripEnum(t *testing.T) {
	reg := tool.NewRegistry()
	if _, err := RegisterUseSkillTool(reg, registries.DefaultSkills(), registries.LoadSkillStack(context.Background(), "", nil).Manager); err != nil {
		t.Fatal(err)
	}
	def, _ := lookupTool(reg, PrimaryToolUseSkill)
	raw, err := json.Marshal(def.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"review"`) || !strings.Contains(string(raw), `"research"`) {
		t.Errorf("marshaled parameters missing skill ids: %s", raw)
	}
}

func lookupTool(reg *tool.Registry, name string) (schema.ToolDef, bool) {
	for _, d := range reg.List() {
		if d.Name == name {
			return d, true
		}
	}
	return schema.ToolDef{}, false
}

func enumOf(def schema.ToolDef, prop string) []string {
	params, _ := def.Parameters.(map[string]any)
	props, _ := params["properties"].(map[string]any)
	field, _ := props[prop].(map[string]any)
	enum, _ := field["enum"].([]string)
	return enum
}

func TestUseSkillTool_RefreshIncludesNewID(t *testing.T) {
	reg := tool.NewRegistry()
	stack := registries.LoadSkillStack(context.Background(), "", nil)
	use, err := RegisterUseSkillTool(reg, stack.Registry, stack.Manager)
	if err != nil {
		t.Fatal(err)
	}
	if err := registries.RegisterFileSkill(stack.Registry, stack.VageRegistry, &skill.Def{
		Name: "hot-skill", Description: "d", Instructions: "i",
	}); err != nil {
		t.Fatal(err)
	}
	wrapped := tool.NewTruncatingToolRegistry(reg, 128)
	if err := use.Refresh(wrapped); err != nil {
		t.Fatal(err)
	}
	def, ok := wrapped.Get(PrimaryToolUseSkill)
	if !ok {
		t.Fatal("use_skill missing after refresh")
	}
	found := false
	for _, id := range enumOf(def, "skill") {
		if id == "hot-skill" {
			found = true
		}
	}
	if !found {
		t.Errorf("enum missing hot-skill: %v", enumOf(def, "skill"))
	}
}
