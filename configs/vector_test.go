package configs

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestVectorConfig_DefaultsOff(t *testing.T) {
	var v VectorConfig
	if v.IsEnabled() {
		t.Error("expected default disabled")
	}
	if v.IsAutoWriteEnabled() {
		t.Error("auto-write must require IsEnabled true")
	}
}

func TestVectorConfig_AutoWriteRequiresEnabled(t *testing.T) {
	tr := true
	v := VectorConfig{AutoWrite: &tr}
	if v.IsAutoWriteEnabled() {
		t.Error("auto-write enabled while subsystem disabled — must short-circuit")
	}
	v.Enabled = &tr
	if !v.IsAutoWriteEnabled() {
		t.Error("auto-write must be enabled when both flags are true")
	}
}

func TestVectorConfig_Effectives(t *testing.T) {
	v := VectorConfig{}
	if got := v.EffectiveBackend(); got != VectorBackendQdrant {
		t.Errorf("EffectiveBackend default = %q, want %q", got, VectorBackendQdrant)
	}
	if got := v.EffectiveEmbedder(); got != VectorEmbedderOpenAI {
		t.Errorf("EffectiveEmbedder default = %q, want %q", got, VectorEmbedderOpenAI)
	}
	if got := v.EffectiveCollection(); got != DefaultVectorCollection {
		t.Errorf("EffectiveCollection default = %q", got)
	}
	if got := v.EffectiveQdrantURL(); got != DefaultQdrantURL {
		t.Errorf("EffectiveQdrantURL default = %q", got)
	}
	if got := v.EffectiveOpenAIModel(); got != DefaultOpenAIEmbedderModel {
		t.Errorf("EffectiveOpenAIModel default = %q", got)
	}

	v = VectorConfig{Backend: "MEMORY", Embedder: "Hash", Collection: " custom ", Qdrant: QdrantConfig{URL: "http://qd:6333"}, OpenAI: OpenAIEmbedderConfig{Model: "text-embedding-3-large"}}
	if got := v.EffectiveBackend(); got != "memory" {
		t.Errorf("EffectiveBackend lowercase = %q", got)
	}
	if got := v.EffectiveEmbedder(); got != "hash" {
		t.Errorf("EffectiveEmbedder lowercase = %q", got)
	}
	if got := v.EffectiveCollection(); got != "custom" {
		t.Errorf("EffectiveCollection trim = %q", got)
	}
	if got := v.EffectiveQdrantURL(); got != "http://qd:6333" {
		t.Errorf("EffectiveQdrantURL = %q", got)
	}
	if got := v.EffectiveOpenAIModel(); got != "text-embedding-3-large" {
		t.Errorf("EffectiveOpenAIModel = %q", got)
	}
}

func TestVectorConfig_Validate_DisabledIsZeroError(t *testing.T) {
	if err := (VectorConfig{}).Validate(); err != nil {
		t.Errorf("disabled config must validate, got %v", err)
	}
}

func TestVectorConfig_Validate_RejectsBadBackend(t *testing.T) {
	tr := true
	v := VectorConfig{Enabled: &tr, Backend: "bogus"}
	err := v.Validate()
	if err == nil || !strings.Contains(err.Error(), "vector.backend") {
		t.Errorf("expected backend validation error, got %v", err)
	}
}

func TestVectorConfig_Validate_RejectsBadEmbedder(t *testing.T) {
	tr := true
	v := VectorConfig{Enabled: &tr, Embedder: "bogus"}
	err := v.Validate()
	if err == nil || !strings.Contains(err.Error(), "vector.embedder") {
		t.Errorf("expected embedder validation error, got %v", err)
	}
}

func TestVectorConfig_Validate_RejectsNegatives(t *testing.T) {
	tr := true
	if err := (VectorConfig{Enabled: &tr, TopK: -1}).Validate(); err == nil {
		t.Error("expected error for negative TopK")
	}
	if err := (VectorConfig{Enabled: &tr, OpenAI: OpenAIEmbedderConfig{Dimensions: -1}}).Validate(); err == nil {
		t.Error("expected error for negative dimensions")
	}
}

func TestVectorConfig_YAMLRoundTrip(t *testing.T) {
	yamlBlob := []byte(`
vector:
  enabled: true
  backend: qdrant
  embedder: openai
  auto_write: true
  top_k: 12
  collection: notes
  qdrant:
    url: http://qd:6333
    api_key: secret
  openai:
    model: text-embedding-3-large
    api_key: sk-x
    dimensions: 768
`)
	var cfg struct {
		Vector VectorConfig `yaml:"vector"`
	}
	if err := yaml.Unmarshal(yamlBlob, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !cfg.Vector.IsEnabled() {
		t.Error("Enabled lost in unmarshal")
	}
	if !cfg.Vector.IsAutoWriteEnabled() {
		t.Error("AutoWrite lost in unmarshal")
	}
	if cfg.Vector.TopK != 12 {
		t.Errorf("TopK = %d", cfg.Vector.TopK)
	}
	if cfg.Vector.Qdrant.URL != "http://qd:6333" {
		t.Errorf("Qdrant.URL = %q", cfg.Vector.Qdrant.URL)
	}
	if cfg.Vector.OpenAI.Model != "text-embedding-3-large" {
		t.Errorf("OpenAI.Model = %q", cfg.Vector.OpenAI.Model)
	}
	if cfg.Vector.OpenAI.Dimensions != 768 {
		t.Errorf("OpenAI.Dimensions = %d", cfg.Vector.OpenAI.Dimensions)
	}
}

func TestVectorConfig_LoadEnvOverrides(t *testing.T) {
	t.Setenv("VV_VECTOR_ENABLED", "true")
	t.Setenv("VV_VECTOR_BACKEND", "memory")
	t.Setenv("VV_VECTOR_EMBEDDER", "hash")
	t.Setenv("VV_VECTOR_AUTO_WRITE", "true")
	t.Setenv("VV_VECTOR_TOP_K", "7")
	t.Setenv("VV_VECTOR_COLLECTION", "envcol")
	t.Setenv("VV_QDRANT_URL", "http://qd:9999")
	t.Setenv("VV_QDRANT_API_KEY", "envkey")
	t.Setenv("VV_VECTOR_OPENAI_API_KEY", "envoa")
	t.Setenv("VV_VECTOR_OPENAI_MODEL", "envmodel")
	t.Setenv("VV_VECTOR_OPENAI_DIMENSIONS", "256")

	dir := t.TempDir()
	path := dir + "/vv.yaml"
	if err := os.WriteFile(path, []byte("llm:\n  api_key: x\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Vector.IsEnabled() {
		t.Error("VV_VECTOR_ENABLED ignored")
	}
	if cfg.Vector.EffectiveBackend() != "memory" {
		t.Errorf("backend = %q", cfg.Vector.EffectiveBackend())
	}
	if cfg.Vector.EffectiveEmbedder() != "hash" {
		t.Errorf("embedder = %q", cfg.Vector.EffectiveEmbedder())
	}
	if !cfg.Vector.IsAutoWriteEnabled() {
		t.Error("auto_write env ignored")
	}
	if cfg.Vector.TopK != 7 {
		t.Errorf("TopK = %d", cfg.Vector.TopK)
	}
	if cfg.Vector.Collection != "envcol" {
		t.Errorf("Collection = %q", cfg.Vector.Collection)
	}
	if cfg.Vector.Qdrant.URL != "http://qd:9999" {
		t.Errorf("Qdrant.URL = %q", cfg.Vector.Qdrant.URL)
	}
	if cfg.Vector.Qdrant.APIKey != "envkey" {
		t.Errorf("Qdrant.APIKey = %q", cfg.Vector.Qdrant.APIKey)
	}
	if cfg.Vector.OpenAI.APIKey != "envoa" {
		t.Errorf("OpenAI.APIKey = %q", cfg.Vector.OpenAI.APIKey)
	}
	if cfg.Vector.OpenAI.Model != "envmodel" {
		t.Errorf("OpenAI.Model = %q", cfg.Vector.OpenAI.Model)
	}
	if cfg.Vector.OpenAI.Dimensions != 256 {
		t.Errorf("OpenAI.Dimensions = %d", cfg.Vector.OpenAI.Dimensions)
	}
}

func TestVectorConfig_LoadOPENAI_API_KEYFallback(t *testing.T) {
	// Explicit YAML key wins over env. Env precedence:
	// VV_VECTOR_OPENAI_API_KEY > OPENAI_API_KEY > unset.
	t.Setenv("VV_VECTOR_OPENAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "fallback-key")

	dir := t.TempDir()
	path := dir + "/vv.yaml"
	if err := os.WriteFile(path, []byte("llm:\n  api_key: x\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vector.OpenAI.APIKey != "fallback-key" {
		t.Errorf("OPENAI_API_KEY fallback failed: %q", cfg.Vector.OpenAI.APIKey)
	}
}

func TestVectorConfig_LoadValidationFails(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/vv.yaml"
	yaml := []byte("llm:\n  api_key: x\nvector:\n  enabled: true\n  backend: bogus\n")
	if err := os.WriteFile(path, yaml, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path, true); err == nil {
		t.Error("expected validation error")
	}
}
