package configs

import (
	"fmt"
	"strings"
)

// Skill evolution eligibility / dedup defaults. YAML 0 (or omitted) is
// filled to these values by applyDefaults; there is no way to disable a
// gate by writing 0.
const (
	DefaultSkillEvolutionMinTurns         = 5
	DefaultSkillEvolutionMinToolSuccessRate = 0.9
	DefaultSkillEvolutionSimilarity       = 0.85
)

// SkillEvolutionConfig is the opt-in skill-from-session pipeline.
// Default-off (CONFIG-R2): enabled=false constructs nothing.
type SkillEvolutionConfig struct {
	Enabled             bool    `yaml:"enabled,omitempty"`               // default false
	MinToolSuccessRate  float64 `yaml:"min_tool_success_rate,omitempty"` // 0 → 0.9
	MinTurns            int     `yaml:"min_turns,omitempty"`             // 0 → 5
	SimilarityThreshold float64 `yaml:"similarity_threshold,omitempty"`  // 0 → 0.85
	AutoApprove         bool    `yaml:"auto_approve,omitempty"`          // must stay false
}

// IsEnabled reports whether skill evolution is turned on.
func (s SkillEvolutionConfig) IsEnabled() bool {
	return s.Enabled
}

func applySkillEvolutionDefaults(s *SkillEvolutionConfig) {
	if s.MinTurns == 0 {
		s.MinTurns = DefaultSkillEvolutionMinTurns
	}
	if s.MinToolSuccessRate == 0 {
		s.MinToolSuccessRate = DefaultSkillEvolutionMinToolSuccessRate
	}
	if s.SimilarityThreshold == 0 {
		s.SimilarityThreshold = DefaultSkillEvolutionSimilarity
	}
}

// ValidateSkillEvolution checks skill_evolution. Zero numeric fields are
// filled to defaults first so Validate() callers that skip Load still pass
// (same pattern as other normalizing validators).
func ValidateSkillEvolution(cfg *Config) error {
	if cfg == nil {
		return nil
	}

	applySkillEvolutionDefaults(&cfg.SkillEvolution)
	s := cfg.SkillEvolution
	if s.AutoApprove {
		return fmt.Errorf("skill_evolution.auto_approve is not supported")
	}

	if s.MinToolSuccessRate <= 0 || s.MinToolSuccessRate > 1 {
		return fmt.Errorf("skill_evolution.min_tool_success_rate must be in (0,1], got %v", s.MinToolSuccessRate)
	}
	if s.MinTurns < 1 {
		return fmt.Errorf("skill_evolution.min_turns must be >= 1, got %d", s.MinTurns)
	}
	if s.SimilarityThreshold <= 0 || s.SimilarityThreshold > 1 {
		return fmt.Errorf("skill_evolution.similarity_threshold must be in (0,1], got %v", s.SimilarityThreshold)
	}

	if !s.Enabled {
		return nil
	}
	if !cfg.Session.IsEnabled() {
		return fmt.Errorf("skill_evolution.enabled requires session.enabled")
	}
	if strings.TrimSpace(cfg.Agents.SkillDir) == "" {
		return fmt.Errorf("skill_evolution.enabled requires agents.skill_dir")
	}

	return nil
}
