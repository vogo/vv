package setup

import (
	"strings"
	"testing"

	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/dispatches"
)

// The Primary reads two contracts that must agree on when a DAG is allowed:
// its system prompt (agents.PrimarySystemPrompt) and the plan_task tool
// description (dispatches.PlanTaskToolDescription). setup is where the two are
// wired onto the same agent, so this is where drift between them is caught —
// a looser tool description would silently cancel the prompt's gate.
var dagGateMarkers = []struct {
	what   string
	marker string
}{
	{"condition (a): at least two independent workflows", "at least two genuinely independent workflows"},
	{"condition (a): not artificial slicing", "not consecutive slices of one change"},
	{"condition (b): real concurrency benefit", "saves real wall-clock time"},
	{"condition (c): dependencies expressible", "depends_on"},
	{"condition (d): user asked for parallel or background", "run in the background"},
	{"counter-example: ordinary bug fix", "ordinary bug fix"},
	{"counter-example: single-file / single-symbol change", "single-file or single-symbol change"},
	{"counter-example: sequential checklist only", "sequential checklist"},
	{"sequential fallback names todo_write", "todo_write"},
	{"advanced-capability examples", "repo-wide migrations"},
}

func TestDAGGate_PromptAndToolContractsAgree(t *testing.T) {
	surfaces := map[string]string{
		"PrimarySystemPrompt":     agents.PrimarySystemPrompt,
		"PlanTaskToolDescription": dispatches.PlanTaskToolDescription,
	}

	for name, text := range surfaces {
		for _, m := range dagGateMarkers {
			if !strings.Contains(text, m.marker) {
				t.Errorf("%s lost %s (missing %q)", name, m.what, m.marker)
			}
		}
	}
}
