package configs

import (
	"strings"
	"testing"
)

// validCfg returns a minimal config that passes Validate, so each test can
// perturb a single field and assert the aggregate entry point reacts.
func validCfg() *Config {
	return &Config{
		CLI:         CLIConfig{PermissionMode: PermissionModeDefault},
		Memory:      MemoryConfig{Backend: MemoryBackendFile},
		Orchestrate: OrchestrateConfig{Mode: OrchestrateModeUnified},
	}
}

func TestValidate_OK(t *testing.T) {
	if err := Validate(validCfg()); err != nil {
		t.Fatalf("expected valid config to pass, got %v", err)
	}
}

func TestValidate_RejectsBadPermissionMode(t *testing.T) {
	c := validCfg()
	c.CLI.PermissionMode = "bogus"
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "invalid permission_mode") {
		t.Fatalf("want permission_mode error, got %v", err)
	}
}

func TestValidate_RejectsBadEval(t *testing.T) {
	c := validCfg()
	c.Eval.Evaluators = []string{"nope"}
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "unknown evaluator") {
		t.Fatalf("want eval error, got %v", err)
	}
}

func TestValidate_RejectsBadMemoryBackend(t *testing.T) {
	c := validCfg()
	c.Memory.Backend = "mongodb"
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "unknown memory backend") {
		t.Fatalf("want memory backend error, got %v", err)
	}
}

func TestValidate_RejectsBadMCPServer(t *testing.T) {
	c := validCfg()
	c.MCP.Server.Transport = "carrier-pigeon"
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("want mcp transport error, got %v", err)
	}
}

func TestValidate_RejectsBadVector(t *testing.T) {
	enabled := true
	c := validCfg()
	c.Vector = VectorConfig{Enabled: &enabled, Backend: "bogus"}
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "vector config") {
		t.Fatalf("want wrapped vector error, got %v", err)
	}
}

// TestValidate_NormalizesInPlace confirms the aggregate entry point still
// writes back the normalization the sub-validators perform: memory backend
// lower-cased/defaulted, orchestrate mode defaulted, and MCP transport
// lower-cased with the http loopback addr default.
func TestValidate_NormalizesInPlace(t *testing.T) {
	c := &Config{
		CLI:         CLIConfig{PermissionMode: PermissionModeDefault},
		Memory:      MemoryConfig{Backend: "  SQLITE  "},
		Orchestrate: OrchestrateConfig{Mode: ""},
		MCP:         MCPConfig{Server: MCPServerConfig{Transport: "HTTP"}},
	}

	if err := Validate(c); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if c.Memory.Backend != MemoryBackendSQLite {
		t.Errorf("memory backend not normalized, got %q", c.Memory.Backend)
	}
	if c.Orchestrate.Mode != OrchestrateModeUnified {
		t.Errorf("orchestrate mode not normalized, got %q", c.Orchestrate.Mode)
	}
	if c.MCP.Server.Transport != "http" {
		t.Errorf("mcp transport not lower-cased, got %q", c.MCP.Server.Transport)
	}
	if c.MCP.Server.Addr != "127.0.0.1:7801" {
		t.Errorf("mcp http addr default not applied, got %q", c.MCP.Server.Addr)
	}
}

// TestValidate_SoftWarnsDoNotError confirms an unknown web_search provider and
// a deprecated confirm_tools list remain warn-only: Validate still returns nil.
func TestValidate_SoftWarnsDoNotError(t *testing.T) {
	c := validCfg()
	c.Tools.WebSearch.Provider = "not-a-provider"
	c.CLI.ConfirmTools = []string{"bash"}

	if err := Validate(c); err != nil {
		t.Fatalf("soft-warn conditions must not error, got %v", err)
	}
}
