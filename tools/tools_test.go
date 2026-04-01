package tools

import (
	"testing"

	"github.com/vogo/vv/configs"
)

func TestRegister_AllRegistered(t *testing.T) {
	reg, err := Register(configs.ToolsConfig{
		BashTimeout:    30,
		BashWorkingDir: "",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	tools := reg.List()
	if len(tools) != 6 {
		t.Fatalf("got %d tools, want 6", len(tools))
	}

	expected := map[string]bool{
		"bash":       false,
		"file_read":  false,
		"file_write": false,
		"file_edit":  false,
		"glob":       false,
		"grep":       false,
	}

	for _, td := range tools {
		if _, ok := expected[td.Name]; !ok {
			t.Errorf("unexpected tool: %s", td.Name)
		} else {
			expected[td.Name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("expected tool %q not registered", name)
		}
	}
}

func TestRegister_CustomBashOptions(t *testing.T) {
	reg, err := Register(configs.ToolsConfig{
		BashTimeout:    120,
		BashWorkingDir: "/tmp",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, ok := reg.Get("bash"); !ok {
		t.Error("bash tool not found in registries")
	}
}

func TestRegister_DefaultConfig(t *testing.T) {
	reg, err := Register(configs.ToolsConfig{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	tools := reg.List()
	if len(tools) != 6 {
		t.Fatalf("got %d tools, want 6", len(tools))
	}
}
