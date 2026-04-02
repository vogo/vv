package dispatches

import (
	"context"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/schema"
)

func TestShouldSummarize_Always(t *testing.T) {
	d := &Dispatcher{summaryPolicy: SummaryAlways}

	if !d.shouldSummarize(&schema.RunRequest{}) {
		t.Error("SummaryAlways should return true")
	}
}

func TestShouldSummarize_Never(t *testing.T) {
	d := &Dispatcher{summaryPolicy: SummaryNever}

	if d.shouldSummarize(&schema.RunRequest{}) {
		t.Error("SummaryNever should return false")
	}
}

func TestShouldSummarize_Auto_HTTP(t *testing.T) {
	d := &Dispatcher{summaryPolicy: SummaryAuto}
	req := &schema.RunRequest{
		Metadata: map[string]any{
			"mode": "http",
		},
	}

	if !d.shouldSummarize(req) {
		t.Error("SummaryAuto with mode=http should return true")
	}
}

func TestShouldSummarize_Auto_CLI(t *testing.T) {
	d := &Dispatcher{summaryPolicy: SummaryAuto}
	req := &schema.RunRequest{
		Metadata: map[string]any{
			"mode": "cli",
		},
	}

	if d.shouldSummarize(req) {
		t.Error("SummaryAuto with mode=cli should return false")
	}
}

func TestShouldSummarize_Auto_NoMetadata(t *testing.T) {
	d := &Dispatcher{summaryPolicy: SummaryAuto}

	if d.shouldSummarize(&schema.RunRequest{}) {
		t.Error("SummaryAuto with no metadata should return false")
	}
}

func TestShouldSummarize_Auto_RequestSummary(t *testing.T) {
	d := &Dispatcher{summaryPolicy: SummaryAuto}
	req := &schema.RunRequest{
		Metadata: map[string]any{
			"request_summary": true,
		},
	}

	if !d.shouldSummarize(req) {
		t.Error("SummaryAuto with request_summary=true should return true")
	}
}

func TestShouldSummarize_Auto_RequestSummary_WithCLIMode(t *testing.T) {
	d := &Dispatcher{summaryPolicy: SummaryAuto}
	req := &schema.RunRequest{
		Metadata: map[string]any{
			"mode":            "cli",
			"request_summary": true,
		},
	}

	if !d.shouldSummarize(req) {
		t.Error("SummaryAuto with mode=cli and request_summary=true should return true")
	}
}

func TestShouldSummarize_EmptyPolicy(t *testing.T) {
	d := &Dispatcher{summaryPolicy: ""}
	req := &schema.RunRequest{
		Metadata: map[string]any{
			"mode": "http",
		},
	}

	if !d.shouldSummarize(req) {
		t.Error("empty policy should default to auto (http=true)")
	}
}

func TestSummarize_WithSummarizer(t *testing.T) {
	summarizer := &stubAgent{
		id: "summarizer",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("summary of results"),
				}, "summarizer"),
			},
		},
	}

	d := &Dispatcher{summarizer: summarizer}

	results := []*schema.RunResponse{
		{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("result 1"),
				}, "agent1"),
			},
		},
	}

	resp, err := d.summarize(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("original request")},
	}, results)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected summary message")
	}

	if resp.Messages[0].Content.Text() != "summary of results" {
		t.Errorf("summary = %q, want %q", resp.Messages[0].Content.Text(), "summary of results")
	}
}

func TestSummarize_NilSummarizer_UsesPlanGen(t *testing.T) {
	planGen := &stubAgent{
		id: "plan-gen",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("planGen summary"),
				}, "plan-gen"),
			},
		},
	}

	d := &Dispatcher{planGen: planGen}

	results := []*schema.RunResponse{
		{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("result"),
				}, "agent"),
			},
		},
	}

	resp, err := d.summarize(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("request")},
	}, results)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if resp.Messages[0].Content.Text() != "planGen summary" {
		t.Errorf("got %q, want %q", resp.Messages[0].Content.Text(), "planGen summary")
	}
}

func TestSummarize_NoSummarizer_ReturnFirstResult(t *testing.T) {
	d := &Dispatcher{}

	results := []*schema.RunResponse{
		{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("first result"),
				}, "agent"),
			},
		},
	}

	resp, err := d.summarize(context.Background(), &schema.RunRequest{}, results)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if resp.Messages[0].Content.Text() != "first result" {
		t.Errorf("got %q, want %q", resp.Messages[0].Content.Text(), "first result")
	}
}
