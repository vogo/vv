package skillevolve

import (
	"context"
	"testing"

	"github.com/vogo/vage/skill"
	"github.com/vogo/vage/vector"
)

func TestLexicalDeduper_SameName(t *testing.T) {
	d := &LexicalDeduper{Threshold: 0.85}
	id, score, ok := d.FindDuplicate(context.Background(), ExtractedSkill{Name: "review", Description: "x"}, []skill.Def{
		{Name: "review", Description: "other"},
	})
	if !ok || id != "review" || score != 1 {
		t.Fatalf("got id=%s score=%v ok=%v", id, score, ok)
	}
}

func TestLexicalDeduper_Jaccard(t *testing.T) {
	d := &LexicalDeduper{Threshold: 0.85}
	listed := []skill.Def{{Name: "old", Description: "review code for security holes and auth bugs"}}
	_, _, ok := d.FindDuplicate(context.Background(), ExtractedSkill{
		Name: "new", Description: "review code for security holes and auth bugs",
	}, listed)
	if !ok {
		t.Fatal("high overlap should duplicate")
	}
	_, _, ok = d.FindDuplicate(context.Background(), ExtractedSkill{
		Name: "new", Description: "write release notes in a cheerful tone",
	}, listed)
	if ok {
		t.Fatal("low overlap should not duplicate")
	}
}

func TestVectorDeduper_NearVectors(t *testing.T) {
	store := vector.NewMapVectorStore()
	near := []float32{1, 0}
	far := []float32{0, 1}
	emb := vector.EmbedderFunc(func(_ context.Context, text string) ([]float32, error) {
		if text == "keep\nbody" {
			return far, nil
		}
		return near, nil
	})
	if err := store.Add(context.Background(), vector.Document{
		ID: "skill:old", Text: "old\nbody", Embedding: near,
		Metadata: map[string]any{"kind": "skill", "name": "old"},
	}); err != nil {
		t.Fatal(err)
	}
	d := &VectorDeduper{Store: store, Embedder: emb, Threshold: 0.85, Lexical: &LexicalDeduper{Threshold: 0.85}}
	id, _, ok := d.FindDuplicate(context.Background(), ExtractedSkill{Name: "new", Description: "dup", Instructions: "body"}, nil)
	if !ok || id != "old" {
		t.Fatalf("near vector: id=%s ok=%v", id, ok)
	}
	id, _, ok = d.FindDuplicate(context.Background(), ExtractedSkill{Name: "keep", Description: "keep", Instructions: "body"}, nil)
	if ok {
		t.Fatalf("orthogonal vector should not duplicate, got %s", id)
	}
}

type spyHash struct {
	*vector.HashEmbedder
	n int
}

func (s *spyHash) Embed(ctx context.Context, text string) ([]float32, error) {
	s.n++
	return s.HashEmbedder.Embed(ctx, text)
}

func TestVectorDeduper_HashUsesLexicalNoEmbed(t *testing.T) {
	spy := &spyHash{HashEmbedder: vector.NewHashEmbedder(8)}
	d := NewDeduper(vector.NewMapVectorStore(), spy, 0.85)
	listed := []skill.Def{{Name: "old", Description: "alpha beta gamma delta"}}
	_, _, _ = d.FindDuplicate(context.Background(), ExtractedSkill{
		Name: "new", Description: "alpha beta gamma delta",
	}, listed)
	if spy.n != 0 {
		t.Fatalf("HashEmbedder path called Embed %d times, want 0", spy.n)
	}
}
