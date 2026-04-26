package tools_tests

import (
	"testing"

	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/tools"
)

func TestIntegration_Tools_AllRegistered(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	toolList := reg.List()
	if len(toolList) != 7 {
		t.Fatalf("registered %d tools, want 7", len(toolList))
	}

	expectedNames := map[string]bool{
		"bash":      false,
		"read":      false,
		"web_fetch": false,
		"write":     false,
		"edit":      false,
		"glob":      false,
		"grep":      false,
	}

	for _, td := range toolList {
		if _, ok := expectedNames[td.Name]; !ok {
			t.Errorf("unexpected tool registered: %q", td.Name)
		} else {
			expectedNames[td.Name] = true
		}
	}

	for name, found := range expectedNames {
		if !found {
			t.Errorf("expected tool %q was not registered", name)
		}
	}
}

func TestIntegration_Tools_BashOptions(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{
		BashTimeout:    120,
		BashWorkingDir: "/tmp",
	})
	if err != nil {
		t.Fatalf("tools.Register with custom options: %v", err)
	}

	if _, ok := reg.Get("bash"); !ok {
		t.Error("bash tool not found after registration with custom options")
	}
}

func TestIntegration_Tools_ZeroConfig(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{})
	if err != nil {
		t.Fatalf("tools.Register with zero config: %v", err)
	}

	if len(reg.List()) != 7 {
		t.Errorf("got %d tools with zero config, want 7", len(reg.List()))
	}
}

// --- Test: RegisterReadOnly creates a registry with exactly 4 read-only tools ---
func TestIntegration_Tools_RegisterReadOnly(t *testing.T) {
	reg, err := tools.RegisterReadOnly(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	toolList := reg.List()
	if len(toolList) != 4 {
		t.Fatalf("read-only registry has %d tools, want 4", len(toolList))
	}

	names := make(map[string]bool)
	for _, td := range toolList {
		names[td.Name] = true
	}

	for _, want := range []string{"read", "web_fetch", "glob", "grep"} {
		if !names[want] {
			t.Errorf("read-only registry missing tool %q", want)
		}
	}

	// Verify dangerous tools are absent.
	for _, absent := range []string{"bash", "write", "edit"} {
		if names[absent] {
			t.Errorf("read-only registry should not have tool %q", absent)
		}
	}
}

// --- Test: RegisterReviewTools creates a registry with exactly 5 tools (read + web fetch + bash) ---
func TestIntegration_Tools_RegisterReviewTools(t *testing.T) {
	reg, err := tools.RegisterReviewTools(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReviewTools: %v", err)
	}

	toolList := reg.List()
	if len(toolList) != 5 {
		t.Fatalf("review registry has %d tools, want 5", len(toolList))
	}

	names := make(map[string]bool)
	for _, td := range toolList {
		names[td.Name] = true
	}

	for _, want := range []string{"bash", "read", "web_fetch", "glob", "grep"} {
		if !names[want] {
			t.Errorf("review registry missing tool %q", want)
		}
	}

	// Verify write/edit tools are absent.
	for _, absent := range []string{"write", "edit"} {
		if names[absent] {
			t.Errorf("review registry should not have tool %q", absent)
		}
	}
}
