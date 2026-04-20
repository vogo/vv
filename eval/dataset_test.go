package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeCaseLine_StringInput(t *testing.T) {
	raw := []byte(`{"id":"c1","input":"hello world","tags":["smoke"]}`)

	c, err := DecodeCaseLine(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if c.ID != "c1" {
		t.Errorf("id = %q", c.ID)
	}

	if c.Input == nil || len(c.Input.Messages) != 1 {
		t.Fatalf("want 1 message, got %+v", c.Input)
	}

	if got := c.Input.Messages[0].Content.Text(); got != "hello world" {
		t.Errorf("content = %q", got)
	}

	if len(c.Tags) != 1 || c.Tags[0] != "smoke" {
		t.Errorf("tags = %v", c.Tags)
	}
}

func TestDecodeCaseLine_ObjectInput(t *testing.T) {
	raw := []byte(`{"id":"c2","input":{"messages":[{"role":"user","content":"explain go"}]},"criteria":["clarity"]}`)

	c, err := DecodeCaseLine(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if c.Input == nil || len(c.Input.Messages) != 1 {
		t.Fatalf("messages missing: %+v", c.Input)
	}

	if got := c.Input.Messages[0].Content.Text(); got != "explain go" {
		t.Errorf("content = %q", got)
	}

	if len(c.Criteria) != 1 {
		t.Errorf("criteria = %v", c.Criteria)
	}
}

func TestDecodeCaseLine_MissingID(t *testing.T) {
	raw := []byte(`{"input":"hi"}`)

	if _, err := DecodeCaseLine(raw); err == nil {
		t.Fatal("want error for missing id")
	}
}

func TestDecodeCaseLine_MissingInput(t *testing.T) {
	raw := []byte(`{"id":"c3"}`)

	if _, err := DecodeCaseLine(raw); err == nil {
		t.Fatal("want error for missing input")
	}
}

func TestDecodeCaseLine_EmptyMessages(t *testing.T) {
	raw := []byte(`{"id":"c4","input":{"messages":[]}}`)

	if _, err := DecodeCaseLine(raw); err == nil {
		t.Fatal("want error for empty messages")
	}
}

func TestDecodeCaseLine_WithExpectedString(t *testing.T) {
	raw := []byte(`{"id":"c5","input":"hi","expected":"hello there"}`)

	c, err := DecodeCaseLine(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if c.Expected == nil || len(c.Expected.Messages) != 1 {
		t.Fatalf("expected missing: %+v", c.Expected)
	}

	if got := c.Expected.Messages[0].Content.Text(); got != "hello there" {
		t.Errorf("expected content = %q", got)
	}
}

func TestLoadJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cases.jsonl")

	content := strings.Join([]string{
		`{"id":"c1","input":"hi"}`,
		``, // blank line — should be ignored
		`{"id":"c2","input":"bye"}`,
		`not valid json`,    // error — should be captured
		`{"input":"no id"}`, // error — missing id
	}, "\n")

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cases, loadErrs, err := LoadJSONL(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(cases) != 2 {
		t.Errorf("cases = %d, want 2", len(cases))
	}

	if len(loadErrs) != 2 {
		t.Errorf("load errors = %d, want 2", len(loadErrs))
	}

	if loadErrs[0].Line != 4 {
		t.Errorf("first err line = %d, want 4", loadErrs[0].Line)
	}

	if loadErrs[1].Line != 5 {
		t.Errorf("second err line = %d, want 5", loadErrs[1].Line)
	}
}

func TestLoadJSONL_NotFound(t *testing.T) {
	if _, _, err := LoadJSONL(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Fatal("want error for missing file")
	}
}
