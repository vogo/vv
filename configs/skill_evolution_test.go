package configs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillEvolution_DefaultDisabled(t *testing.T) {
	cfg := loadYAML(t, "")
	if cfg.SkillEvolution.Enabled {
		t.Fatal("skill_evolution.enabled must default false")
	}
	if cfg.SkillEvolution.MinTurns != DefaultSkillEvolutionMinTurns {
		t.Errorf("min_turns = %d, want %d", cfg.SkillEvolution.MinTurns, DefaultSkillEvolutionMinTurns)
	}
	if cfg.SkillEvolution.MinToolSuccessRate != DefaultSkillEvolutionMinToolSuccessRate {
		t.Errorf("min_tool_success_rate = %v, want %v", cfg.SkillEvolution.MinToolSuccessRate, DefaultSkillEvolutionMinToolSuccessRate)
	}
	if cfg.SkillEvolution.SimilarityThreshold != DefaultSkillEvolutionSimilarity {
		t.Errorf("similarity_threshold = %v, want %v", cfg.SkillEvolution.SimilarityThreshold, DefaultSkillEvolutionSimilarity)
	}
}

func TestSkillEvolution_YAMLZeroFillsDefaults(t *testing.T) {
	cfg := loadYAML(t, `
skill_evolution:
  min_tool_success_rate: 0
  min_turns: 0
  similarity_threshold: 0
`)
	if cfg.SkillEvolution.MinToolSuccessRate != 0.9 {
		t.Errorf("min_tool_success_rate = %v, want 0.9", cfg.SkillEvolution.MinToolSuccessRate)
	}
	if cfg.SkillEvolution.MinTurns != 5 {
		t.Errorf("min_turns = %d, want 5", cfg.SkillEvolution.MinTurns)
	}
	if cfg.SkillEvolution.SimilarityThreshold != 0.85 {
		t.Errorf("similarity_threshold = %v, want 0.85", cfg.SkillEvolution.SimilarityThreshold)
	}
}

func TestSkillEvolution_ValidateRequiresSessionAndSkillDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")

	off := `
session:
  enabled: false
agents:
  skill_dir: /tmp/skills
skill_evolution:
  enabled: true
`
	if err := os.WriteFile(path, []byte(off), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, true); err == nil || !strings.Contains(err.Error(), "session.enabled") {
		t.Fatalf("want session.enabled error, got %v", err)
	}

	noDir := `
skill_evolution:
  enabled: true
`
	if err := os.WriteFile(path, []byte(noDir), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, true); err == nil || !strings.Contains(err.Error(), "agents.skill_dir") {
		t.Fatalf("want agents.skill_dir error, got %v", err)
	}
}

func TestSkillEvolution_AutoApproveRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")
	if err := os.WriteFile(path, []byte("skill_evolution:\n  auto_approve: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, true); err == nil || !strings.Contains(err.Error(), "auto_approve") {
		t.Fatalf("want auto_approve error, got %v", err)
	}
}

func TestSkillEvolution_InvalidRate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")
	if err := os.WriteFile(path, []byte("skill_evolution:\n  min_tool_success_rate: 1.5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, true); err == nil {
		t.Fatal("expected Validate error for min_tool_success_rate 1.5")
	}
}

func TestSkillEvolution_EnvOverridesYAMLFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")
	content := `
agents:
  skill_dir: ` + dir + `
skill_evolution:
  enabled: false
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VV_SKILL_EVOLUTION_ENABLED", "true")
	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.SkillEvolution.Enabled {
		t.Fatal("VV_SKILL_EVOLUTION_ENABLED=true should override YAML enabled: false")
	}
}
