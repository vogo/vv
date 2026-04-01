package registries

import (
	"testing"

	"github.com/vogo/vv/configs"
)

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

	tools := reg.List()
	if len(tools) != 6 {
		t.Errorf("full profile tools = %d, want 6", len(tools))
	}
}

func TestToolProfile_BuildRegistry_ReadOnly(t *testing.T) {
	reg, err := ProfileReadOnly.BuildRegistry(configs.ToolsConfig{BashTimeout: 10})
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}

	tools := reg.List()
	if len(tools) != 3 {
		t.Errorf("read-only profile tools = %d, want 3", len(tools))
	}
}

func TestToolProfile_BuildRegistry_Review(t *testing.T) {
	reg, err := ProfileReview.BuildRegistry(configs.ToolsConfig{BashTimeout: 10})
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}

	tools := reg.List()
	if len(tools) != 4 {
		t.Errorf("review profile tools = %d, want 4", len(tools))
	}
}

func TestToolProfile_BuildRegistry_None(t *testing.T) {
	reg, err := ProfileNone.BuildRegistry(configs.ToolsConfig{BashTimeout: 10})
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}

	tools := reg.List()
	if len(tools) != 0 {
		t.Errorf("none profile tools = %d, want 0", len(tools))
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

	tools := reg.List()
	if len(tools) != 6 {
		t.Errorf("full profile tools (with working dir) = %d, want 6", len(tools))
	}
}
