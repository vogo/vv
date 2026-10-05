package agents

import (
	"testing"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vv/registries"
)

func TestRegisterPrimary_Descriptor(t *testing.T) {
	reg := registries.New()
	RegisterPrimary(reg)

	desc, ok := reg.Get(PrimaryAgentID)
	if !ok {
		t.Fatalf("primary descriptor not registered under ID %q", PrimaryAgentID)
	}

	if desc.ID != PrimaryAgentID {
		t.Errorf("ID = %q, want %q", desc.ID, PrimaryAgentID)
	}

	if desc.Dispatchable {
		t.Error("Primary must be non-dispatchable (only invoked by dispatcher in unified mode)")
	}

	if desc.ToolProfile.Name != registries.ProfileReadOnly.Name {
		t.Errorf("ToolProfile = %q, want %q", desc.ToolProfile.Name, registries.ProfileReadOnly.Name)
	}

	if desc.SystemPrompt == "" {
		t.Error("SystemPrompt must be populated")
	}

	if desc.Factory == nil {
		t.Error("Factory must be set")
	}
}

func TestRegisterPrimary_FactoryBuildsAgent(t *testing.T) {
	reg := registries.New()
	RegisterPrimary(reg)

	desc, _ := reg.Get(PrimaryAgentID)

	a, err := desc.Factory(registries.FactoryOptions{
		Model:         "test-model",
		MaxIterations: 5,
	})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}

	if a == nil {
		t.Fatal("Factory returned nil agent")
	}

	if a.ID() != PrimaryAgentID {
		t.Errorf("agent ID = %q, want %q", a.ID(), PrimaryAgentID)
	}

	// The Primary Assistant must implement StreamAgent for compatibility with
	// the dispatcher's streaming path.
	if _, ok := a.(agent.StreamAgent); !ok {
		t.Error("Primary Assistant must implement agent.StreamAgent")
	}
}

func TestPrimarySystemPrompt_MentionsTools(t *testing.T) {
	// Guard against prompt drift — the rules reference delegate_to_ and
	// plan_task by name; losing either is a prompt regression.
	if !contains(PrimarySystemPrompt, "delegate_to_") {
		t.Error("system prompt lost the delegate_to_ reference")
	}

	if !contains(PrimarySystemPrompt, "plan_task") {
		t.Error("system prompt lost the plan_task reference")
	}

	if !contains(PrimarySystemPrompt, "todo_write") {
		t.Error("system prompt lost the todo_write reference")
	}

	if !contains(PrimarySystemPrompt, "memory_set") {
		t.Error("system prompt lost the memory_set reference")
	}

	if !contains(PrimarySystemPrompt, "ask_user") {
		t.Error("system prompt lost the ask_user reference")
	}

	if !contains(PrimarySystemPrompt, "use_skill") {
		t.Error("system prompt lost the use_skill clause")
	}

	if !contains(PrimarySystemPrompt, "Human approval") {
		t.Error("system prompt lost the interrupt / human-approval clause")
	}
}

// TestPrimarySystemPrompt_DAGGate pins the decision contract that demotes DAG
// planning to an explicit advanced capability: the prompt must state all four
// enabling conditions, declare sequential execution the default, and name the
// counter-examples (ordinary bug fix / single-file change) that must stay off
// the plan_task path.
func TestPrimarySystemPrompt_DAGGate(t *testing.T) {
	// One entry per required condition so a failure names the missing clause
	// rather than just "prompt changed".
	required := []struct {
		what   string
		marker string
	}{
		{"condition (a): at least two independent workflows", "at least two genuinely independent workflows"},
		{"condition (a): not artificial slicing", "not consecutive slices of one change"},
		{"condition (b): real concurrency benefit", "saves real wall-clock time"},
		{"condition (c): dependencies expressible", "depends_on"},
		{"condition (d): user asked for parallel or background", "run in the background"},
		{"sequential execution is the default", "Sequential execution is the default"},
		{"progress is tracked with todo_write, not a DAG", "todo_write"},
		{"counter-example: ordinary bug fix", "ordinary bug fix"},
		{"counter-example: single-file / single-symbol change", "single-file or single-symbol change"},
		{"counter-example: sequential checklist only", "sequential checklist"},
		// The step-count bound lives only in the prompt contract:
		// ClassifyResult.validate checks agent identity, not plan size, so
		// dropping this phrasing removes the only cap on DAG width.
		{"step-count bound", "2-5 steps"},
	}

	for _, r := range required {
		if !contains(PrimarySystemPrompt, r.marker) {
			t.Errorf("system prompt lost %s (missing %q)", r.what, r.marker)
		}
	}

	// Regression guard: the pre-demotion phrasing let "spans multiple
	// specialist capabilities" alone justify a DAG. Reintroducing it (or an
	// equivalent loosening) would undo the gate above.
	forbidden := []string{
		"genuinely spans multiple specialist capabilities",
		"when the task genuinely spans multiple",
	}

	for _, f := range forbidden {
		if contains(PrimarySystemPrompt, f) {
			t.Errorf("system prompt re-introduced the loosened planning trigger %q", f)
		}
	}
}

// TestPrimarySystemPrompt_RouteSnapshot is a decision snapshot: for each
// scenario the prompt must carry the clause that governs it. It asserts the
// contract the model reads, not the model's output — the mock LLM used in the
// integration suite returns pre-canned tool calls and so cannot exercise the
// routing decision itself.
func TestPrimarySystemPrompt_RouteSnapshot(t *testing.T) {
	scenarios := []struct {
		scenario      string
		expectedRoute string
		governing     string
	}{
		{
			scenario:      "ordinary bug fix",
			expectedRoute: "sequential execution by Primary",
			governing:     "An ordinary bug fix, a single-file or single-symbol change",
		},
		{
			scenario:      "single-file edit with several sub-steps",
			expectedRoute: "sequential execution by Primary + todo_write",
			governing:     "Sequential execution is the default",
		},
		{
			scenario:      "multi-step work that merely touches several capabilities",
			expectedRoute: "sequential execution, or one delegation",
			governing:     `"It has several steps" or "it spans several capabilities" is by itself not a reason to plan.`,
		},
		{
			scenario:      "one isolated sub-task needing a specialist",
			expectedRoute: "single delegation",
			governing:     "Prefer a single delegation over a multi-step plan",
		},
		{
			scenario:      "user asks for parallel work across two independent branches",
			expectedRoute: "plan_task DAG",
			governing:     "the user asked for parallel execution",
		},
		{
			scenario:      "long task the user wants running in the background",
			expectedRoute: "plan_task DAG",
			governing:     "asked for a long task to run in the background",
		},
	}

	for _, s := range scenarios {
		if !contains(PrimarySystemPrompt, s.governing) {
			t.Errorf("scenario %q (expected route: %s): prompt lost its governing clause %q",
				s.scenario, s.expectedRoute, s.governing)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}

	return -1
}
