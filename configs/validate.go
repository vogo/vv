package configs

import (
	"fmt"
	"log/slog"
	"strings"
)

// Validate is the single aggregate entry point for full-config validation.
// Load calls it exactly once, after applyDefaults, so every caller that goes
// through Load receives a normalized, validated *Config.
//
// It coordinates the existing per-subsystem validators in a fixed order —
// permission mode, MCP server, eval, memory backend, orchestrate mode and
// vector — returning the first error encountered. Validators that also
// normalize (memory backend, orchestrate mode, MCP transport/addr) write their
// result back onto cfg. Soft-warn-only checks (deprecated confirm_tools,
// unknown web_search provider) are emitted here too; they never escalate to an
// error, preserving the pre-refactor behaviour.
func Validate(cfg *Config) error {
	if !IsValidPermissionMode(cfg.CLI.PermissionMode) {
		return fmt.Errorf("invalid permission_mode %q; valid values: default, accept-edits, auto, plan",
			cfg.CLI.PermissionMode)
	}

	if err := ValidateMCPServer(&cfg.MCP.Server); err != nil {
		return err
	}

	if err := ValidateEval(&cfg.Eval); err != nil {
		return err
	}

	normalizedBackend, err := ValidateMemoryBackend(cfg.Memory.Backend)
	if err != nil {
		return err
	}
	cfg.Memory.Backend = normalizedBackend

	normalizedMode, err := ValidateOrchestrateMode(cfg.Orchestrate.Mode)
	if err != nil {
		return err
	}
	cfg.Orchestrate.Mode = normalizedMode

	if err := cfg.Vector.Validate(); err != nil {
		return fmt.Errorf("vector config: %w", err)
	}

	warnDeprecatedAndSoftInvalid(cfg)

	return nil
}

// warnDeprecatedAndSoftInvalid emits the load-time soft warnings that do not
// abort startup: the deprecated confirm_tools list and an unrecognized
// web_search provider (which disables the tool rather than failing).
func warnDeprecatedAndSoftInvalid(cfg *Config) {
	if len(cfg.CLI.ConfirmTools) > 0 {
		slog.Warn("vv: confirm_tools is deprecated; use permission_mode instead")
	}

	if raw := strings.TrimSpace(cfg.Tools.WebSearch.Provider); raw != "" && NormalizedWebSearchProvider(raw) == "" {
		slog.Warn("vv: tools.web_search.provider is not recognized; tool will be disabled",
			"value", raw, "valid", []string{WebSearchProviderTavily, WebSearchProviderBrave, WebSearchProviderSearXNG})
	}
}
