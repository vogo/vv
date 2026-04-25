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
