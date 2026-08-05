package registries

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func stubSource(id, body string) ContextSource {
	return ContextSource{
		ID:          id,
		Description: id + " description",
		Provider:    func(context.Context) (string, error) { return body, nil },
	}
}

func TestContextSourceRegistry_RegisterErrors(t *testing.T) {
	reg := NewContextSources()

	if err := reg.Register(ContextSource{ID: " ", Provider: func(context.Context) (string, error) { return "", nil }}); err == nil {
		t.Error("expected error for empty source ID")
	}

	if err := reg.Register(ContextSource{ID: "x"}); err == nil {
		t.Error("expected error for nil provider")
	}

	reg.MustRegister(stubSource("x", "body"))

	if err := reg.Register(stubSource("x", "other")); err == nil {
		t.Error("expected duplicate source ID to be rejected")
	}
}

func TestContextSourceRegistry_Resolve(t *testing.T) {
	reg := NewContextSources()
	reg.MustRegister(stubSource("diff", "DIFF-BODY"))
	reg.MustRegister(stubSource("plan", "PLAN-BODY"))
	reg.MustRegister(ContextSource{
		ID:          "broken",
		Provider:    func(context.Context) (string, error) { return "", errors.New("boom") },
		Description: "always fails",
	})

	t.Run("empty list yields empty block", func(t *testing.T) {
		got, err := reg.Resolve(context.Background(), nil)
		if err != nil || got != "" {
			t.Fatalf("Resolve(nil) = %q, %v; want empty, nil", got, err)
		}
	})

	t.Run("labelled read-only blocks in order", func(t *testing.T) {
		got, err := reg.Resolve(context.Background(), []string{"diff", "plan"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := "## Context: diff (read-only)\n\nDIFF-BODY\n\n## Context: plan (read-only)\n\nPLAN-BODY"
		if got != want {
			t.Errorf("Resolve() = %q, want %q", got, want)
		}
	})

	t.Run("unknown source is an error", func(t *testing.T) {
		if _, err := reg.Resolve(context.Background(), []string{"nope"}); err == nil {
			t.Error("expected error for unregistered context source")
		}
	})

	// A failing source must abort, not silently hand the worker a context it
	// was told to read but never got.
	t.Run("provider failure propagates", func(t *testing.T) {
		_, err := reg.Resolve(context.Background(), []string{"broken"})
		if err == nil || !strings.Contains(err.Error(), "broken") {
			t.Errorf("Resolve() error = %v, want it to name the failing source", err)
		}
	})
}

func TestDefaultContextSources_RegistersDiff(t *testing.T) {
	reg := DefaultContextSources("")

	if !reg.ValidateRef(ContextSourceDiff) {
		t.Fatal("built-in diff context source not registered")
	}

	if got := reg.IDs(); len(got) != 1 || got[0] != ContextSourceDiff {
		t.Errorf("IDs() = %v, want [diff]", got)
	}
}

func TestGitDiffProvider(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(
			os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	file := filepath.Join(dir, "a.txt")

	run("init")

	if err := os.WriteFile(file, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	run("add", "a.txt")
	run("commit", "-m", "init")

	provider := GitDiffProvider(dir)

	t.Run("clean tree", func(t *testing.T) {
		got, err := provider(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(got, "无差异") {
			t.Errorf("clean tree diff = %q, want the empty-diff notice", got)
		}
	})

	t.Run("modified tree", func(t *testing.T) {
		if err := os.WriteFile(file, []byte("changed\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		got, err := provider(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, want := range []string{"```diff", "a.txt", "-original", "+changed"} {
			if !strings.Contains(got, want) {
				t.Errorf("diff missing %q; got:\n%s", want, got)
			}
		}
	})

	t.Run("non-repository is a diagnosable error", func(t *testing.T) {
		outside := GitDiffProvider(t.TempDir())

		if _, err := outside(context.Background()); err == nil {
			t.Error("expected error outside a git repository")
		}
	})
}

// Truncation must not split a multi-byte rune: a naive byte slice would put
// invalid UTF-8 into the prompt.
func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		maxBytes  int
		wantCut   bool
		wantKeeps string
	}{
		{name: "under limit is untouched", in: "hello", maxBytes: 10},
		{name: "at limit is untouched", in: "hello", maxBytes: 5},
		{name: "ascii cut", in: "abcdefghij", maxBytes: 4, wantCut: true, wantKeeps: "abcd"},
		// "中" is 3 bytes; cutting at 4 must fall back to the rune boundary at 3.
		{name: "multi-byte cut falls back to boundary", in: "中文内容", maxBytes: 4, wantCut: true, wantKeeps: "中"},
		{name: "multi-byte exact boundary", in: "中文内容", maxBytes: 6, wantCut: true, wantKeeps: "中文"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateUTF8(tt.in, tt.maxBytes)

			if !utf8.ValidString(got) {
				t.Fatalf("truncateUTF8 produced invalid UTF-8: %q", got)
			}

			if !tt.wantCut {
				if got != tt.in {
					t.Errorf("got %q, want the input unchanged", got)
				}

				return
			}

			if !strings.HasPrefix(got, tt.wantKeeps) {
				t.Errorf("got %q, want it to start with %q", got, tt.wantKeeps)
			}

			if !strings.Contains(got, "已截断") {
				t.Errorf("truncation not announced in-band: %q", got)
			}
		})
	}
}
