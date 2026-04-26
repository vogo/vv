package tools

import (
	"fmt"
	"time"

	"github.com/vogo/vage/tool"
	"github.com/vogo/vage/tool/websearch"
	"github.com/vogo/vv/configs"
)

// MaybeRegisterWebSearch installs the web_search tool on reg when cfg carries
// a recognized provider id and an api key. Otherwise it is a no-op so the
// "no key configured" path stays cost-free (the tool never appears in any
// agent's ToolDef list). Returns the resolved provider name on success or ""
// when wiring was skipped.
//
// Centralized here so the read-capability (vv/registries/tool_access.go) and
// the three flat factories below share one source of truth for the
// Provider/Option construction.
func MaybeRegisterWebSearch(reg *tool.Registry, cfg configs.WebSearchConfig) (string, error) {
	if !cfg.IsEnabled() {
		return "", nil
	}

	provider := buildWebSearchProvider(cfg)
	if provider == nil {
		return "", nil
	}

	opts := []websearch.Option{websearch.WithProvider(provider)}
	if cfg.TimeoutSeconds > 0 {
		opts = append(opts, websearch.WithTimeout(time.Duration(cfg.TimeoutSeconds)*time.Second))
	}
	if cfg.MaxResults > 0 {
		opts = append(opts, websearch.WithDefaultMaxResults(cfg.MaxResults))
	}

	if err := websearch.Register(reg, opts...); err != nil {
		return "", fmt.Errorf("register web_search: %w", err)
	}
	return provider.Name(), nil
}

// buildWebSearchProvider maps the validated provider id to its concrete
// implementation. Returns nil for an empty / unknown id (already filtered by
// IsEnabled, so this branch is defensive only).
//
// The intermediate concrete-typed locals matter: assigning a nil concrete
// pointer (e.g. when NewTavily rejects an empty key) directly into the
// websearch.Provider interface would yield a non-nil interface with a nil
// concrete value — the caller's `provider == nil` guard would not catch it
// and provider.Name() would panic on first use. By checking each typed local
// before returning, we ensure the interface return is honestly nil.
func buildWebSearchProvider(cfg configs.WebSearchConfig) websearch.Provider {
	switch configs.NormalizedWebSearchProvider(cfg.Provider) {
	case configs.WebSearchProviderTavily:
		if p := websearch.NewTavily(cfg.APIKey); p != nil {
			return p
		}
		return nil
	case configs.WebSearchProviderBrave:
		if p := websearch.NewBrave(cfg.APIKey); p != nil {
			return p
		}
		return nil
	default:
		return nil
	}
}
