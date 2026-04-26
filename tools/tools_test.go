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
	if len(tools) != 7 {
		t.Fatalf("got %d tools, want 7", len(tools))
	}

	expected := map[string]bool{
		"bash":      false,
		"read":      false,
		"web_fetch": false,
		"write":     false,
		"edit":      false,
		"glob":      false,
		"grep":      false,
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
	if len(tools) != 7 {
		t.Fatalf("got %d tools, want 7", len(tools))
	}
}

// scenario: when web_search is configured, all three factories include it.
func TestRegister_WithWebSearchEnabled(t *testing.T) {
	cfg := configs.ToolsConfig{
		BashTimeout: 30,
		WebSearch: configs.WebSearchConfig{
			Provider: configs.WebSearchProviderTavily,
			APIKey:   "test-key",
		},
	}

	full, err := Register(cfg)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := full.Get("web_search"); !ok {
		t.Errorf("Register: web_search missing")
	}
	if got := len(full.List()); got != 8 {
		t.Errorf("Register tool count = %d, want 8", got)
	}

	ro, err := RegisterReadOnly(cfg)
	if err != nil {
		t.Fatalf("RegisterReadOnly: %v", err)
	}
	if _, ok := ro.Get("web_search"); !ok {
		t.Errorf("RegisterReadOnly: web_search missing")
	}

	rv, err := RegisterReviewTools(cfg)
	if err != nil {
		t.Fatalf("RegisterReviewTools: %v", err)
	}
	if _, ok := rv.Get("web_search"); !ok {
		t.Errorf("RegisterReviewTools: web_search missing")
	}
}

// scenario (defensive): even if IsEnabled were ever to misfire and admit a
// blank/whitespace key, buildWebSearchProvider must still return an honest
// nil so MaybeRegisterWebSearch's nil guard catches it. Guards against the
// classic typed-nil-interface trap.
func TestBuildWebSearchProvider_NilSafe(t *testing.T) {
	cfg := configs.WebSearchConfig{
		Provider: configs.WebSearchProviderTavily,
		APIKey:   "   ", // whitespace — NewTavily returns nil pointer
	}
	got := buildWebSearchProvider(cfg)
	if got != nil {
		t.Fatalf("expected honest nil interface, got non-nil %T", got)
	}
}

// scenario: an unknown provider id is treated as "not configured" — tool
// must not appear (avoiding a broken handler in agent ToolDef lists).
func TestRegister_WebSearchUnknownProvider(t *testing.T) {
	cfg := configs.ToolsConfig{
		WebSearch: configs.WebSearchConfig{
			Provider: "google",
			APIKey:   "x",
		},
	}
	reg, err := Register(cfg)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := reg.Get("web_search"); ok {
		t.Fatal("web_search should not be registered for unknown provider")
	}
}
