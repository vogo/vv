package configs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentsConfig_SkillDirYAMLAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")
	content := `llm:
  provider: openai
  model: test-model
  api_key: test-key
  base_url: http://127.0.0.1:0
agents:
  skill_dir: /from/yaml
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agents.SkillDir != "/from/yaml" {
		t.Errorf("SkillDir = %q, want /from/yaml", cfg.Agents.SkillDir)
	}

	t.Setenv("VV_AGENTS_SKILL_DIR", "/from/env")
	cfg, err = Load(path, true)
	if err != nil {
		t.Fatalf("Load with env: %v", err)
	}
	if cfg.Agents.SkillDir != "/from/env" {
		t.Errorf("SkillDir = %q, want env override /from/env", cfg.Agents.SkillDir)
	}
}
