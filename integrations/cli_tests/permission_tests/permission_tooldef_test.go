package permission_tests

import (
	"testing"
	"time"

	"github.com/vogo/vage/tool/askuser"
	"github.com/vogo/vage/tool/bash"
	"github.com/vogo/vage/tool/edit"
	"github.com/vogo/vage/tool/glob"
	"github.com/vogo/vage/tool/grep"
	"github.com/vogo/vage/tool/read"
	"github.com/vogo/vage/tool/write"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/tools"
)

// TestIntegration_ToolDef_ReadOnly_ReadTool verifies that the read tool's
// ToolDef declares ReadOnly: true.
func TestIntegration_ToolDef_ReadOnly_ReadTool(t *testing.T) {
	rt := read.New()
	def := rt.ToolDef()

	if !def.ReadOnly {
		t.Errorf("read ToolDef().ReadOnly = false, want true")
	}
}

// TestIntegration_ToolDef_ReadOnly_GlobTool verifies that the glob tool's
// ToolDef declares ReadOnly: true.
func TestIntegration_ToolDef_ReadOnly_GlobTool(t *testing.T) {
	gt := glob.New()
	def := gt.ToolDef()

	if !def.ReadOnly {
		t.Errorf("glob ToolDef().ReadOnly = false, want true")
	}
}

// TestIntegration_ToolDef_ReadOnly_GrepTool verifies that the grep tool's
// ToolDef declares ReadOnly: true.
func TestIntegration_ToolDef_ReadOnly_GrepTool(t *testing.T) {
	gt := grep.New()
	def := gt.ToolDef()

	if !def.ReadOnly {
		t.Errorf("grep ToolDef().ReadOnly = false, want true")
	}
}

// TestIntegration_ToolDef_ReadOnly_AskUserTool verifies that the ask_user tool's
// ToolDef declares ReadOnly: true.
func TestIntegration_ToolDef_ReadOnly_AskUserTool(t *testing.T) {
	at := askuser.New(askuser.NonInteractiveInteractor{})
	def := at.ToolDef()

	if !def.ReadOnly {
		t.Errorf("ask_user ToolDef().ReadOnly = false, want true")
	}
}

// TestIntegration_ToolDef_ReadOnly_WriteTool verifies that the write tool's
// ToolDef declares ReadOnly: false (default, non-read-only).
func TestIntegration_ToolDef_ReadOnly_WriteTool(t *testing.T) {
	wt := write.New()
	def := wt.ToolDef()

	if def.ReadOnly {
		t.Errorf("write ToolDef().ReadOnly = true, want false")
	}
}

// TestIntegration_ToolDef_ReadOnly_EditTool verifies that the edit tool's
// ToolDef declares ReadOnly: false (default, non-read-only).
func TestIntegration_ToolDef_ReadOnly_EditTool(t *testing.T) {
	et := edit.New()
	def := et.ToolDef()

	if def.ReadOnly {
		t.Errorf("edit ToolDef().ReadOnly = true, want false")
	}
}

// TestIntegration_ToolDef_ReadOnly_BashTool verifies that the bash tool's
// ToolDef declares ReadOnly: false (default, non-read-only).
func TestIntegration_ToolDef_ReadOnly_BashTool(t *testing.T) {
	bt := bash.New(bash.WithTimeout(5 * time.Second))
	def := bt.ToolDef()

	if def.ReadOnly {
		t.Errorf("bash ToolDef().ReadOnly = true, want false")
	}
}

// TestIntegration_ToolDef_ReadOnly_RegistryReflectsReadOnly verifies that after
// registering tools in a real registry, the Get method returns the correct
// ReadOnly value for each tool.
func TestIntegration_ToolDef_ReadOnly_RegistryReflectsReadOnly(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyTools := map[string]bool{
		"read": true,
		"glob": true,
		"grep": true,
	}
	writeTools := map[string]bool{
		"write": false,
		"edit":  false,
		"bash":  false,
	}

	for name, wantReadOnly := range readOnlyTools {
		def, ok := reg.Get(name)
		if !ok {
			t.Errorf("tool %q not found in registry", name)
			continue
		}

		if def.ReadOnly != wantReadOnly {
			t.Errorf("registry Get(%q).ReadOnly = %v, want %v", name, def.ReadOnly, wantReadOnly)
		}
	}

	for name, wantReadOnly := range writeTools {
		def, ok := reg.Get(name)
		if !ok {
			t.Errorf("tool %q not found in registry", name)
			continue
		}

		if def.ReadOnly != wantReadOnly {
			t.Errorf("registry Get(%q).ReadOnly = %v, want %v", name, def.ReadOnly, wantReadOnly)
		}
	}
}
