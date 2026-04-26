package registries

import (
	"sort"
	"testing"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
)

// toolNames returns the sorted set of tool names in defs.
func toolNames(defs []schema.ToolDef) []string {
	out := make([]string, len(defs))
	for i, d := range defs {
		out[i] = d.Name
	}
	sort.Strings(out)
	return out
}

func assertToolNames(t *testing.T, defs []schema.ToolDef, want ...string) {
	t.Helper()
	got := toolNames(defs)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("tool names = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("tool names = %v, want %v", got, want)
		}
	}
}

func TestToolProfile_Has(t *testing.T) {
	tests := []struct {
		profile ToolProfile
		cap     ToolCapability
		want    bool
	}{
		{ProfileFull, CapRead, true},
		{ProfileFull, CapWrite, true},
		{ProfileFull, CapExecute, true},
		{ProfileFull, CapSearch, true},
		{ProfileReadOnly, CapRead, true},
		{ProfileReadOnly, CapSearch, true},
		{ProfileReadOnly, CapWrite, false},
		{ProfileReadOnly, CapExecute, false},
		{ProfileReview, CapRead, true},
		{ProfileReview, CapSearch, true},
		{ProfileReview, CapExecute, true},
		{ProfileReview, CapWrite, false},
		{ProfileNone, CapRead, false},
		{ProfileNone, CapWrite, false},
	}

	for _, tt := range tests {
		got := tt.profile.Has(tt.cap)
		if got != tt.want {
			t.Errorf("%s.Has(%s) = %v, want %v", tt.profile.Name, tt.cap, got, tt.want)
		}
	}
}

func TestProfileByName(t *testing.T) {
	tests := []struct {
		name    string
		wantOK  bool
		wantCap int // number of capabilities in the profile
	}{
		{"full", true, 4},
		{"read-only", true, 2},
		{"review", true, 3},
		{"none", true, 0},
		{"unknown", false, 0},
		{"", false, 0},
	}

	for _, tt := range tests {
		profile, ok := ProfileByName(tt.name)
		if ok != tt.wantOK {
			t.Errorf("ProfileByName(%q) ok = %v, want %v", tt.name, ok, tt.wantOK)
		}

		if ok && len(profile.Capabilities) != tt.wantCap {
			t.Errorf("ProfileByName(%q) caps = %d, want %d", tt.name, len(profile.Capabilities), tt.wantCap)
		}
	}
}

func TestToolProfile_BuildRegistry_Full(t *testing.T) {
	reg, err := ProfileFull.BuildRegistry(configs.ToolsConfig{BashTimeout: 10})
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}

	assertToolNames(t, reg.List(), "bash", "edit", "glob", "grep", "read", "web_fetch", "write")
}

func TestToolProfile_BuildRegistry_ReadOnly(t *testing.T) {
	reg, err := ProfileReadOnly.BuildRegistry(configs.ToolsConfig{BashTimeout: 10})
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}

	assertToolNames(t, reg.List(), "glob", "grep", "read", "web_fetch")
}

func TestToolProfile_BuildRegistry_Review(t *testing.T) {
	reg, err := ProfileReview.BuildRegistry(configs.ToolsConfig{BashTimeout: 10})
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}

	assertToolNames(t, reg.List(), "bash", "glob", "grep", "read", "web_fetch")
}

func TestToolProfile_BuildRegistry_None(t *testing.T) {
	reg, err := ProfileNone.BuildRegistry(configs.ToolsConfig{BashTimeout: 10})
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}

	if len(reg.List()) != 0 {
		t.Errorf("none profile tools = %v, want []", toolNames(reg.List()))
	}
}

func TestToolProfile_BuildRegistry_WithWorkingDir(t *testing.T) {
	reg, err := ProfileFull.BuildRegistry(configs.ToolsConfig{
		BashTimeout:    10,
		BashWorkingDir: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}

	assertToolNames(t, reg.List(), "bash", "edit", "glob", "grep", "read", "web_fetch", "write")
}
