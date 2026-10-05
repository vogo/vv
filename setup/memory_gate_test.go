package setup

import (
	"strings"
	"testing"

	memtool "github.com/vogo/vage/tool/memory"
	"github.com/vogo/vv/agents"
)

// Primary reads two contracts that must agree on when a durable fact may be
// written: its system prompt and the memory_set tool description. setup is
// where both land on the same agent, so drift is caught here.
var memoryWriteGateMarkers = []struct {
	what   string
	marker string
}{
	{"user corrected an existing convention", "corrected an existing convention"},
	{"stable project/personal preference", "stable project/personal preference"},
	{"failure root cause reused across sessions", "root cause is worth reusing across sessions"},
	{"never write process intermediates", "Never write process intermediates"},
	{"recall tool named", "memory_recall"},
}

func TestMemoryWriteGate_PromptAndToolContractsAgree(t *testing.T) {
	if !strings.Contains(agents.PrimarySystemPrompt, "memory_recall") {
		t.Error("PrimarySystemPrompt lost memory_recall")
	}
	if !strings.Contains(memtool.SetToolDescription, "memory_recall") {
		t.Error("SetToolDescription lost memory_recall")
	}

	for _, m := range memoryWriteGateMarkers {
		if m.marker == "memory_recall" {
			continue
		}
		if !strings.Contains(agents.PrimarySystemPrompt, m.marker) {
			t.Errorf("PrimarySystemPrompt lost %s (missing %q)", m.what, m.marker)
		}
		if !strings.Contains(memtool.SetToolDescription, m.marker) {
			t.Errorf("SetToolDescription lost %s (missing %q)", m.what, m.marker)
		}
	}
}
