package registry

import (
	"strings"
	"testing"

	"github.com/vogo/vage/agent"
)

func TestRegistry_Register_Get(t *testing.T) {
	reg := New()
	err := reg.Register(AgentDescriptor{
		ID:           "test",
		DisplayName:  "Test",
		Description:  "A test agent",
		Dispatchable: true,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	desc, ok := reg.Get("test")
	if !ok {
		t.Fatal("expected to find 'test'")
	}

	if desc.ID != "test" {
		t.Errorf("ID = %q, want %q", desc.ID, "test")
	}
}

func TestRegistry_Register_Duplicate(t *testing.T) {
	reg := New()
	_ = reg.Register(AgentDescriptor{ID: "test"})

	err := reg.Register(AgentDescriptor{ID: "test"})
	if err == nil {
		t.Fatal("expected error on duplicate")
	}

	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error = %q, want to contain 'duplicate'", err.Error())
	}
}

func TestRegistry_MustRegister_Duplicate_Panics(t *testing.T) {
	reg := New()
	reg.MustRegister(AgentDescriptor{ID: "test"})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate MustRegister")
		}
	}()

	reg.MustRegister(AgentDescriptor{ID: "test"})
}

func TestRegistry_Get_NotFound(t *testing.T) {
	reg := New()

	_, ok := reg.Get("nonexistent")
	if ok {
		t.Error("expected not found")
	}
}

func TestRegistry_All_Sorted(t *testing.T) {
	reg := New()
	reg.MustRegister(AgentDescriptor{ID: "charlie"})
	reg.MustRegister(AgentDescriptor{ID: "alpha"})
	reg.MustRegister(AgentDescriptor{ID: "bravo"})

	all := reg.All()
	if len(all) != 3 {
		t.Fatalf("All() = %d, want 3", len(all))
	}

	if all[0].ID != "alpha" || all[1].ID != "bravo" || all[2].ID != "charlie" {
		t.Errorf("All() not sorted: %v, %v, %v", all[0].ID, all[1].ID, all[2].ID)
	}
}

func TestRegistry_Dispatchable(t *testing.T) {
	reg := New()
	reg.MustRegister(AgentDescriptor{ID: "coder", Dispatchable: true})
	reg.MustRegister(AgentDescriptor{ID: "explorer", Dispatchable: false})
	reg.MustRegister(AgentDescriptor{ID: "chat", Dispatchable: true})

	dispatchable := reg.Dispatchable()
	if len(dispatchable) != 2 {
		t.Fatalf("Dispatchable() = %d, want 2", len(dispatchable))
	}

	// Should be sorted.
	if dispatchable[0].ID != "chat" || dispatchable[1].ID != "coder" {
		t.Errorf("Dispatchable() = %v, %v; want chat, coder", dispatchable[0].ID, dispatchable[1].ID)
	}
}

func TestRegistry_ValidateRef(t *testing.T) {
	reg := New()
	reg.MustRegister(AgentDescriptor{ID: "coder"})

	if !reg.ValidateRef("coder") {
		t.Error("expected 'coder' to be valid")
	}

	if reg.ValidateRef("nonexistent") {
		t.Error("expected 'nonexistent' to be invalid")
	}
}

func TestRegistry_PlannerAgentList(t *testing.T) {
	reg := New()
	reg.MustRegister(AgentDescriptor{
		ID: "coder", DisplayName: "Coder",
		Description: "Reads, writes, edits files", Dispatchable: true,
	})
	reg.MustRegister(AgentDescriptor{
		ID: "researcher", DisplayName: "Researcher",
		Description: "Explores codebases", Dispatchable: true,
	})
	reg.MustRegister(AgentDescriptor{
		ID: "explorer", DisplayName: "Explorer",
		Description: "Infrastructure", Dispatchable: false,
	})

	list := reg.PlannerAgentList()

	if !strings.Contains(list, `"coder"`) {
		t.Error("expected list to contain coder")
	}

	if !strings.Contains(list, `"researcher"`) {
		t.Error("expected list to contain researcher")
	}

	// Explorer is not dispatchable, should not appear.
	if strings.Contains(list, `"explorer"`) {
		t.Error("expected list to NOT contain explorer")
	}
}

func TestFactoryOptions(t *testing.T) {
	// Verify FactoryOptions struct fields compile.
	opts := FactoryOptions{
		Model:         "test-model",
		MaxIterations: 10,
	}

	if opts.Model != "test-model" {
		t.Errorf("Model = %q", opts.Model)
	}
}

func TestAgentDescriptor_Factory(t *testing.T) {
	desc := AgentDescriptor{
		ID: "test",
		Factory: func(opts FactoryOptions) (agent.Agent, error) {
			return nil, nil
		},
	}

	_, err := desc.Factory(FactoryOptions{})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
}
