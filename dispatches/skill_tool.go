package dispatches

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/skill"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/registries"
)

// PrimaryToolUseSkill is the Primary-only tool that activates a registered
// skill for the current session. Instructions take effect on the next turn.
const PrimaryToolUseSkill = "use_skill"

// UseSkillTool holds the use_skill handler so Refresh can overlay ToolDef
// without reconstructing the closure.
type UseSkillTool struct {
	skills  *registries.SkillRegistry
	mgr     skill.Manager
	handler tool.ToolHandler
}

// RegisterUseSkillTool installs the `use_skill` tool onto reg. Failures are
// returned as IsError tool results, never as handler errors.
func RegisterUseSkillTool(reg tool.ToolRegistry, skills *registries.SkillRegistry, mgr skill.Manager) (*UseSkillTool, error) {
	if mgr == nil {
		return nil, fmt.Errorf("use_skill: skill manager is required")
	}

	ids := []string{}
	if skills != nil {
		ids = skills.IDs()
	}

	handler := newUseSkillHandler(skills, mgr)
	def := schema.ToolDef{
		Name:        PrimaryToolUseSkill,
		Description: useSkillDescription(ids),
		Parameters:  useSkillParameters(ids),
		Source:      schema.ToolSourceLocal,
	}

	if err := registerIfAbsent(reg, def, handler); err != nil {
		return nil, fmt.Errorf("register use_skill tool: %w", err)
	}

	return &UseSkillTool{skills: skills, mgr: mgr, handler: handler}, nil
}

// Refresh overlays Parameters/Description from the live skill registry, keeping
// the original handler. Must be called on the wrapped registry TaskAgent holds.
func (t *UseSkillTool) Refresh(reg tool.ToolRegistry) error {
	if t == nil {
		return nil
	}
	if _, ok := reg.Get(PrimaryToolUseSkill); !ok {
		return fmt.Errorf("use_skill: not registered")
	}
	ids := []string{}
	if t.skills != nil {
		ids = t.skills.IDs()
	}
	def := schema.ToolDef{
		Name:        PrimaryToolUseSkill,
		Description: useSkillDescription(ids),
		Parameters:  useSkillParameters(ids),
		Source:      schema.ToolSourceLocal,
	}
	return reg.Register(def, t.handler)
}

func useSkillDescription(ids []string) string {
	var sb strings.Builder
	sb.WriteString("Activate a registered skill for this session. The skill's instructions ")
	sb.WriteString("are injected into the system prompt on the NEXT turn, not the current one. ")
	sb.WriteString("Skills never grant tools and never widen permission. ")
	sb.WriteString("Activating an already-active skill succeeds without error (idempotent).")
	if len(ids) > 0 {
		sb.WriteString(" Available: ")
		sb.WriteString(strings.Join(ids, ", "))
		sb.WriteString(".")
	}

	return sb.String()
}

func useSkillParameters(ids []string) map[string]any {
	skillParam := map[string]any{
		"type":        "string",
		"description": "Skill ID to activate. Takes effect on the next turn; the current turn's system prompt and tool set stay unchanged.",
	}
	if len(ids) > 0 {
		skillParam["enum"] = ids
	}

	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill": skillParam,
		},
		"required": []string{"skill"},
	}
}

type useSkillArgs struct {
	Skill string `json:"skill"`
}

func newUseSkillHandler(skills *registries.SkillRegistry, mgr skill.Manager) tool.ToolHandler {
	return func(ctx context.Context, _ string, args string) (schema.ToolResult, error) {
		var parsed useSkillArgs
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			return schema.ErrorResult("", "use_skill: invalid arguments: "+err.Error()), nil
		}

		id := strings.TrimSpace(parsed.Skill)
		if id == "" {
			return schema.ErrorResult("", "use_skill: 'skill' must be a non-empty string"), nil
		}

		available := availableSkillIDs(skills)
		if skills != nil && !skills.ValidateRef(id) {
			return schema.ErrorResult("", fmt.Sprintf("use_skill: unknown skill %q; available: %s", id, available)), nil
		}

		sessionID := schema.SessionIDFromContext(ctx)
		for _, act := range mgr.ActiveSkills(sessionID) {
			if act.SkillName == id {
				return schema.TextResult("", fmt.Sprintf(
					"Skill %q is already active for this session. Its instructions apply from the next turn; this turn is unchanged.",
					id,
				)), nil
			}
		}

		if _, err := mgr.Activate(ctx, id, sessionID); err != nil {
			msg := "use_skill: " + err.Error()
			if available != "" {
				msg += "; available: " + available
			}
			return schema.ErrorResult("", msg), nil
		}

		return schema.TextResult("", fmt.Sprintf(
			"Skill %q activated. Its instructions will be injected into the system prompt on the next turn, not this one. The tool set is unchanged.",
			id,
		)), nil
	}
}

func availableSkillIDs(skills *registries.SkillRegistry) string {
	if skills == nil {
		return ""
	}
	return strings.Join(skills.IDs(), ", ")
}
