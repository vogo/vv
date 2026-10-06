package skillevolve

import (
	"context"
	"strings"
	"unicode"

	"github.com/vogo/vage/skill"
	"github.com/vogo/vage/vector"
)

// LexicalDeduper uses exact name match then description token Jaccard.
type LexicalDeduper struct {
	Threshold float32
}

// FindDuplicate implements Deduper.
func (d *LexicalDeduper) FindDuplicate(_ context.Context, extracted ExtractedSkill, listed []skill.Def) (string, float32, bool) {
	if id, score, ok := nameDuplicate(extracted, listed); ok {
		return id, score, true
	}
	th := d.Threshold
	if th <= 0 {
		th = 0.85
	}
	tokens := tokenize(extracted.Description)
	bestID := ""
	var best float32
	for _, def := range listed {
		score := jaccard(tokens, tokenize(def.Description))
		if score >= th && score >= best {
			best = score
			bestID = def.Name
		}
	}
	if bestID == "" {
		return "", 0, false
	}
	return bestID, best, true
}

// VectorDeduper embeds Description+Instructions when a real embedder is
// available; HashEmbedder (or a missing pair) falls back to lexical and
// never calls Embed.
type VectorDeduper struct {
	Store     vector.VectorStore
	Embedder  vector.Embedder
	Lexical   *LexicalDeduper
	Threshold float32
}

// FindDuplicate implements Deduper.
func (d *VectorDeduper) FindDuplicate(ctx context.Context, extracted ExtractedSkill, listed []skill.Def) (string, float32, bool) {
	if id, score, ok := nameDuplicate(extracted, listed); ok {
		return id, score, true
	}
	lex := d.Lexical
	if lex == nil {
		lex = &LexicalDeduper{Threshold: d.Threshold}
	}
	if d.Store == nil || d.Embedder == nil || isHashEmbedder(d.Embedder) {
		return lex.FindDuplicate(ctx, extracted, listed)
	}
	th := d.Threshold
	if th <= 0 {
		th = 0.85
	}
	vec, err := d.Embedder.Embed(ctx, extracted.Description+"\n"+extracted.Instructions)
	if err != nil {
		return lex.FindDuplicate(ctx, extracted, listed)
	}
	hits, err := d.Store.Search(ctx, vec, vector.SearchOptions{
		TopK:           3,
		MinScore:       th,
		MetadataEquals: map[string]any{"kind": "skill"},
	})
	if err != nil || len(hits) == 0 {
		return "", 0, false
	}
	hit := hits[0]
	name, _ := hit.Document.Metadata["name"].(string)
	if name == "" {
		name = strings.TrimPrefix(hit.Document.ID, "skill:")
	}
	return name, hit.Score, true
}

// NewDeduper picks vector (when a non-hash embedder is present) or lexical.
func NewDeduper(store vector.VectorStore, emb vector.Embedder, threshold float32) Deduper {
	lex := &LexicalDeduper{Threshold: threshold}
	if store != nil && emb != nil {
		return &VectorDeduper{Store: store, Embedder: emb, Lexical: lex, Threshold: threshold}
	}
	return lex
}

func nameDuplicate(extracted ExtractedSkill, listed []skill.Def) (string, float32, bool) {
	for _, def := range listed {
		if def.Name == extracted.Name {
			return def.Name, 1, true
		}
	}
	return "", 0, false
}

func isHashEmbedder(emb vector.Embedder) bool {
	if emb == nil {
		return false
	}
	if _, ok := emb.(*vector.HashEmbedder); ok {
		return true
	}
	if n, ok := emb.(vector.NamedEmbedder); ok && n.ModelName() == vector.HashEmbedderModelName {
		return true
	}
	return false
}

func tokenize(s string) map[string]struct{} {
	out := map[string]struct{}{}
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		out[b.String()] = struct{}{}
		b.Reset()
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func jaccard(a, b map[string]struct{}) float32 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if _, ok := b[t]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float32(inter) / float32(union)
}

func listedFromRegistry(reg skill.Registry) []skill.Def {
	if reg == nil {
		return nil
	}
	raw := reg.List()
	out := make([]skill.Def, 0, len(raw))
	for _, d := range raw {
		if d != nil {
			out = append(out, *d)
		}
	}
	return out
}
