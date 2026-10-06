package skillevolve

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/hook"
	"github.com/vogo/vage/skill"
	"github.com/vogo/vage/vector"
	"github.com/vogo/vv/registries"
)

// Registrar writes an approved skill to disk and hot-registers it.
type Registrar struct {
	SkillDir  string
	VV        *registries.SkillRegistry
	Vage      skill.Registry
	Refresher SchemaRefresher
	Store     vector.VectorStore
	Embedder  vector.Embedder
}

// Register writes SKILL.md, loads it, RegisterFileSkill, then Refresh.
// Disk is rolled back on register failure; Refresh failure is logged only.
func (r *Registrar) Register(ctx context.Context, s ExtractedSkill) (basePath string, err error) {
	if r == nil {
		return "", fmt.Errorf("skill registrar is nil")
	}
	basePath, created, err := WriteSkillMD(r.SkillDir, s)
	if err != nil {
		return "", err
	}
	rollback := func() {
		if created {
			_ = os.RemoveAll(basePath)
		}
	}
	def, err := (&skill.FileLoader{}).Load(ctx, basePath)
	if err != nil {
		rollback()
		return "", err
	}
	if err := registries.RegisterFileSkill(r.VV, r.Vage, def); err != nil {
		rollback()
		return "", err
	}
	if r.Refresher != nil {
		if err := r.Refresher.Refresh(); err != nil {
			slog.Error("vv: skill schema refresh failed after register", "skill", s.Name, "error", err)
		}
	}
	r.maybeIndex(ctx, s)
	return basePath, nil
}

func (r *Registrar) maybeIndex(ctx context.Context, s ExtractedSkill) {
	if r.Store == nil || r.Embedder == nil || isHashEmbedder(r.Embedder) {
		return
	}
	vec, err := r.Embedder.Embed(ctx, s.Description+"\n"+s.Instructions)
	if err != nil {
		slog.Warn("vv: skill vector embed failed", "skill", s.Name, "error", err)
		return
	}
	err = r.Store.Add(ctx, vector.Document{
		ID:        "skill:" + s.Name,
		Text:      s.Description + "\n" + s.Instructions,
		Embedding: vec,
		Metadata:  map[string]any{"kind": "skill", "name": s.Name},
	})
	if err != nil {
		slog.Warn("vv: skill vector add failed", "skill", s.Name, "error", err)
	}
}

func dispatchSkillEvent(ctx context.Context, hooks *hook.Manager, sessionID, name, proposalID, skillName, status string) {
	if ctx == nil {
		ctx = context.Background()
	}
	hooks.Dispatch(ctx, schema.NewEvent(schema.EventCustom, "", sessionID, schema.CustomEventData{
		Name: name,
		Payload: map[string]any{
			"proposal_id": proposalID,
			"skill":       skillName,
			"status":      status,
		},
	}))
}
