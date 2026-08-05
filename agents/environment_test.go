package agents

import (
	"strings"
	"testing"
)

func TestAppendEnvironment_EmptyLeavesPromptUnchanged(t *testing.T) {
	t.Parallel()

	if got := AppendEnvironment("base", ""); got != "base" {
		t.Errorf("AppendEnvironment = %q, want %q", got, "base")
	}
}

// TestComposeSystemPrompt_Order pins the layering: base prompt, then
// environment, then project instructions last — the user-authored rules sit
// closest to the end of the prompt, where they carry the most weight.
func TestComposeSystemPrompt_Order(t *testing.T) {
	t.Parallel()

	got := ComposeSystemPrompt("BASE", "- Working directory: /repo\n", "PROJECT RULES")

	baseIdx := strings.Index(got, "BASE")
	envIdx := strings.Index(got, "Working directory")
	projIdx := strings.Index(got, "PROJECT RULES")

	if baseIdx < 0 || envIdx < 0 || projIdx < 0 {
		t.Fatalf("composed prompt missing a section:\n%s", got)
	}

	if baseIdx >= envIdx || envIdx >= projIdx {
		t.Errorf("section order = base:%d env:%d project:%d, want base < env < project", baseIdx, envIdx, projIdx)
	}

	if !strings.Contains(got, "# Environment") {
		t.Errorf("composed prompt missing the Environment heading:\n%s", got)
	}
}

func TestComposeSystemPrompt_BothEmpty(t *testing.T) {
	t.Parallel()

	if got := ComposeSystemPrompt("BASE", "", ""); got != "BASE" {
		t.Errorf("ComposeSystemPrompt = %q, want %q", got, "BASE")
	}
}
