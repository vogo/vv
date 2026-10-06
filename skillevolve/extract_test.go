package skillevolve

import (
	"context"
	"strings"
	"testing"

	largemodel "github.com/vogo/largemodel/model"
	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/skill"
)

type fakeCaller struct {
	replies []string
	n       int
}

func (f *fakeCaller) Protocol() schema.Protocol { return schema.ProtocolOpenAIChat }
func (f *fakeCaller) Call(_ context.Context, _ *largemodel.Request) (*largemodel.Response, error) {
	if f.n >= len(f.replies) {
		return &largemodel.Response{Message: schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleAssistant, "not-json")}, nil
	}
	text := f.replies[f.n]
	f.n++
	return &largemodel.Response{Message: schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleAssistant, text)}, nil
}
func (f *fakeCaller) CallStream(context.Context, *largemodel.Request) (*largemodel.Stream, error) {
	return nil, nil
}

func TestNormalize_NameKebabAndBuiltin(t *testing.T) {
	feat := SessionFeatures{SessionID: "s1"}
	got, err := normalizeExtracted(ExtractedSkill{Name: "Go_Test", Description: "d", Instructions: "i"}, feat)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "go-test" {
		t.Errorf("Name = %q, want go-test", got.Name)
	}

	got, err = normalizeExtracted(ExtractedSkill{Name: "review", Description: "d", Instructions: "i"}, feat)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "review-evolved" {
		t.Errorf("Name = %q, want review-evolved", got.Name)
	}
	if err := skill.ValidateName(got.Name); err != nil {
		t.Fatal(err)
	}
}

func TestNormalize_PrivacyStrip(t *testing.T) {
	token := "UNIQUESECRET99"
	feat := SessionFeatures{SessionID: "s1", UserTask: "please handle " + token + " carefully"}
	got, err := normalizeExtracted(ExtractedSkill{
		Name: "x", Description: "d",
		Instructions: "When the user says " + token + " do the generic thing.",
	}, feat)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Instructions, token) {
		t.Errorf("instructions leaked token: %q", got.Instructions)
	}
}

func TestNormalize_PrivacyStripDescription(t *testing.T) {
	token := "UNIQUESECRET99"
	feat := SessionFeatures{SessionID: "s1", UserTask: "please handle " + token + " carefully"}
	got, err := normalizeExtracted(ExtractedSkill{
		Name: "x", Description: "workflow involving " + token,
		Instructions: "Do the generic thing.",
	}, feat)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Description, token) {
		t.Errorf("description leaked token: %q", got.Description)
	}
	if strings.Contains(got.Instructions, token) {
		t.Errorf("instructions leaked token: %q", got.Instructions)
	}
	if rendered := renderSkillMarkdown(got); strings.Contains(rendered, token) {
		t.Errorf("rendered markdown leaked token:\n%s", rendered)
	}
}

func TestNormalize_SplitTokenNotStripped(t *testing.T) {
	token := "UNIQUESECRET99"
	split := "UNIQUE SECRET99"
	feat := SessionFeatures{SessionID: "s1", UserTask: token}
	got, err := normalizeExtracted(ExtractedSkill{
		Name: "x", Description: "d", Instructions: "rewrite as " + split,
	}, feat)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Instructions, "UNIQUE") {
		t.Errorf("split rewrite should remain, got %q", got.Instructions)
	}
}

func TestNormalize_LineAndByteCaps(t *testing.T) {
	feat := SessionFeatures{SessionID: "s1"}
	lines := make([]string, 501)
	for i := range lines {
		lines[i] = "line"
	}
	got, err := normalizeExtracted(ExtractedSkill{
		Name: "x", Description: "d", Instructions: strings.Join(lines, "\n"),
	}, feat)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got.Instructions, "\n") + 1; n > 500 {
		t.Errorf("instruction lines = %d, want <= 500", n)
	}

	huge := strings.Repeat("abcdefghij ", 6000)
	got, err = normalizeExtracted(ExtractedSkill{Name: "x", Description: "d", Instructions: huge}, feat)
	if err != nil {
		t.Fatal(err)
	}
	if len(renderSkillMarkdown(got)) > maxSkillMDBytes {
		t.Errorf("rendered size %d > 50KiB", len(renderSkillMarkdown(got)))
	}
}

func TestLLMExtractor_InvalidJSONTwice(t *testing.T) {
	ex := NewLLMExtractor(&fakeCaller{replies: []string{"not-json", "still-not"}}, "m")
	_, err := ex.Extract(context.Background(), SessionFeatures{SessionID: "s", UserTask: "task"})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestHeuristicExtractor_ValidName(t *testing.T) {
	ex := NewHeuristicExtractor()
	got, err := ex.Extract(context.Background(), SessionFeatures{
		SessionID: "s",
		ToolSequence: []ToolStep{
			{Name: "grep", AgentID: "primary"},
			{Name: "grep", AgentID: "primary"},
			{Name: "read", AgentID: "primary"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := skill.ValidateName(got.Name); err != nil {
		t.Fatalf("Name %q: %v", got.Name, err)
	}
	if strings.TrimSpace(got.Instructions) == "" {
		t.Fatal("empty instructions")
	}
}
