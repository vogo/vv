package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vogo/vv/configs"
)

// TestBuildEnvironment_CarriesRuntimeFacts locks in the facts an agent cannot
// derive on its own. The working directory line is the one that failed in
// production: without it the front door opened its turn with `read(".")`,
// which the file tools reject outright.
func TestBuildEnvironment_CarriesRuntimeFacts(t *testing.T) {
	dir := t.TempDir()

	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &configs.Config{
		Tools:                   configs.ToolsConfig{BashWorkingDir: dir},
		ProjectInstructionsFile: "AGENTS.md",
	}

	got := buildEnvironment(cfg, 24)

	for _, want := range []string{
		"Working directory: " + dir,
		"absolute paths",
		"Platform:",
		"Today's date:",
		"Git repository: yes",
		"Project instructions file: AGENTS.md",
		"Tool-iteration budget: 24",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("environment block missing %q:\n%s", want, got)
		}
	}
}

// TestBuildEnvironment_OmitsBudgetWhenUnset covers the fallback Primary, which
// runs one tool-free turn: a budget hint there would be noise.
func TestBuildEnvironment_OmitsBudgetWhenUnset(t *testing.T) {
	cfg := &configs.Config{Tools: configs.ToolsConfig{BashWorkingDir: t.TempDir()}}

	got := buildEnvironment(cfg, 0)
	if strings.Contains(got, "Tool-iteration budget") {
		t.Errorf("environment block = %q, want no budget line", got)
	}

	if strings.Contains(got, "Git repository: yes") {
		t.Errorf("environment block = %q, want a non-git temp dir reported as no", got)
	}
}

func TestBuildEnvironment_NilConfig(t *testing.T) {
	if got := buildEnvironment(nil, 10); got != "" {
		t.Errorf("buildEnvironment(nil) = %q, want empty", got)
	}
}
