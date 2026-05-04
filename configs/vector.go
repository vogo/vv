package configs

import (
	"fmt"
	"strings"
)

// VectorConfig controls the vector recall subsystem (vage/vector +
// vage/context.VectorRecallSource + auto-write hook + vector_search /
// vector_add LLM tools + HTTP /v1/vector endpoints).
//
// Default-off: vector recall is opt-in because it requires either a
// running qdrant instance or an API key for the embedder; the default
// vv install should not try to phone home.
type VectorConfig struct {
	// Enabled toggles the entire subsystem on. When false, no store /
	// embedder is constructed, no Source is appended, no tools are
	// registered, and the HTTP routes return 503. Default false.
	Enabled *bool `yaml:"enabled,omitempty"`

	// Backend selects the VectorStore implementation:
	//   - "qdrant" (default): vage/vector/qdrant — REST client, requires Qdrant.URL.
	//   - "memory": vage/vector.MapVectorStore — in-process map. For demos
	//     and tests; no persistence, vectors lost on restart.
	Backend string `yaml:"backend,omitempty"`

	// Embedder selects the Embedder implementation:
	//   - "openai" (default): vage/vector/openai — text-embedding-3 family,
	//     requires OpenAI.APIKey or VV_VECTOR_OPENAI_API_KEY.
	//   - "hash": vage/vector.HashEmbedder — deterministic FNV bag-of-tokens.
	//     For tests / offline demos only; no semantic understanding.
	Embedder string `yaml:"embedder,omitempty"`

	// AutoWrite enables vage/vector/archivehook so the agent's final
	// EventAgentEnd message is auto-indexed. Off by default — users opt
	// in once they have observed the recall path is healthy.
	AutoWrite *bool `yaml:"auto_write,omitempty"`

	// TopK is the default number of hits VectorRecallSource and the
	// vector_search tool ask for. 0 falls back to vector.DefaultTopK.
	TopK int `yaml:"top_k,omitempty"`

	// Collection is the qdrant collection (or logical "namespace" for the
	// in-memory backend). Defaults to "vv_default" so a fresh install has
	// a sensible namespace without forcing the user to invent one.
	Collection string `yaml:"collection,omitempty"`

	Qdrant QdrantConfig         `yaml:"qdrant,omitempty"`
	OpenAI OpenAIEmbedderConfig `yaml:"openai,omitempty"`
}

// QdrantConfig holds qdrant-specific connection details.
type QdrantConfig struct {
	URL    string `yaml:"url,omitempty"`     // default http://localhost:6333; env VV_QDRANT_URL
	APIKey string `yaml:"api_key,omitempty"` // env VV_QDRANT_API_KEY
}

// OpenAIEmbedderConfig holds OpenAI-embedding-API configuration.
//
// APIKey is read with the following precedence at Load time: explicit
// vector.openai.api_key in YAML > VV_VECTOR_OPENAI_API_KEY env >
// OPENAI_API_KEY env. We intentionally do NOT fall back to
// VV_LLM_API_KEY: the LLM key may be for an Anthropic-only or
// non-OpenAI provider, where the embedding endpoint will reject it
// with a confusing 4xx.
type OpenAIEmbedderConfig struct {
	Model      string `yaml:"model,omitempty"`      // default text-embedding-3-small
	APIKey     string `yaml:"api_key,omitempty"`    // env VV_VECTOR_OPENAI_API_KEY / OPENAI_API_KEY
	BaseURL    string `yaml:"base_url,omitempty"`   // OpenAI-compatible providers
	Dimensions int    `yaml:"dimensions,omitempty"` // 0 -> server default for the chosen model
}

// Vector backend / embedder identifiers.
const (
	VectorBackendQdrant = "qdrant"
	VectorBackendMemory = "memory"

	VectorEmbedderOpenAI = "openai"
	VectorEmbedderHash   = "hash"

	// DefaultVectorCollection is used when cfg.Vector.Collection is empty.
	DefaultVectorCollection = "vv_default"

	// DefaultQdrantURL is the conventional local-dev endpoint produced by
	// `docker run -p 6333:6333 qdrant/qdrant`.
	DefaultQdrantURL = "http://localhost:6333"

	// DefaultOpenAIEmbedderModel matches openai.DefaultModel — duplicated as a
	// const here so the configs package does not import vage/vector/openai.
	DefaultOpenAIEmbedderModel = "text-embedding-3-small"
)

// IsEnabled reports whether the subsystem should be wired. Default false.
func (v VectorConfig) IsEnabled() bool {
	return v.Enabled != nil && *v.Enabled
}

// IsAutoWriteEnabled reports whether the AgentEnd auto-write hook should
// be installed. Implies IsEnabled (returns false when subsystem is off).
func (v VectorConfig) IsAutoWriteEnabled() bool {
	if !v.IsEnabled() {
		return false
	}
	return v.AutoWrite != nil && *v.AutoWrite
}

// EffectiveBackend returns the canonical, lowercased backend identifier.
// Empty falls back to VectorBackendQdrant — the only truly production
// option in this release. Memory backend must be explicitly opted into.
func (v VectorConfig) EffectiveBackend() string {
	b := strings.ToLower(strings.TrimSpace(v.Backend))
	if b == "" {
		return VectorBackendQdrant
	}
	return b
}

// EffectiveEmbedder returns the canonical, lowercased embedder
// identifier. Empty falls back to VectorEmbedderOpenAI; users opting
// out of network calls must explicitly set "hash".
func (v VectorConfig) EffectiveEmbedder() string {
	e := strings.ToLower(strings.TrimSpace(v.Embedder))
	if e == "" {
		return VectorEmbedderOpenAI
	}
	return e
}

// EffectiveCollection returns the configured collection or
// DefaultVectorCollection.
func (v VectorConfig) EffectiveCollection() string {
	c := strings.TrimSpace(v.Collection)
	if c == "" {
		return DefaultVectorCollection
	}
	return c
}

// EffectiveQdrantURL returns the configured URL or the local-dev
// default. Used at wiring time when the qdrant backend is selected.
func (v VectorConfig) EffectiveQdrantURL() string {
	u := strings.TrimSpace(v.Qdrant.URL)
	if u == "" {
		return DefaultQdrantURL
	}
	return u
}

// EffectiveOpenAIModel returns the configured model or the default.
func (v VectorConfig) EffectiveOpenAIModel() string {
	m := strings.TrimSpace(v.OpenAI.Model)
	if m == "" {
		return DefaultOpenAIEmbedderModel
	}
	return m
}

// Validate checks consistency. Called from Load after env-var overrides
// are applied so misconfigurations surface at startup.
//
// We deliberately do NOT validate API keys here — empty key is a
// soft-fail (subsystem disables itself with a slog.Warn at wiring
// time) so a YAML with `vector.enabled: true` but no API key in scope
// for a `vv help` invocation does not break the binary.
func (v VectorConfig) Validate() error {
	if !v.IsEnabled() {
		return nil
	}
	switch v.EffectiveBackend() {
	case VectorBackendQdrant, VectorBackendMemory:
	default:
		return fmt.Errorf(
			"vector.backend %q invalid (expected %q or %q)",
			v.Backend, VectorBackendQdrant, VectorBackendMemory,
		)
	}
	switch v.EffectiveEmbedder() {
	case VectorEmbedderOpenAI, VectorEmbedderHash:
	default:
		return fmt.Errorf(
			"vector.embedder %q invalid (expected %q or %q)",
			v.Embedder, VectorEmbedderOpenAI, VectorEmbedderHash,
		)
	}
	if v.TopK < 0 {
		return fmt.Errorf("vector.top_k must be >= 0, got %d", v.TopK)
	}
	if v.OpenAI.Dimensions < 0 {
		return fmt.Errorf("vector.openai.dimensions must be >= 0, got %d", v.OpenAI.Dimensions)
	}
	return nil
}
