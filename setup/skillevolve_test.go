package setup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/registries"
	"github.com/vogo/vv/sessionlogs"
	"github.com/vogo/vv/skillevolve"
)

func TestInstallSkillEvolution_DisabledIsNoop(t *testing.T) {
	ev, err := installSkillEvolution(&configs.Config{}, nil, nil, &mockChatCompleter{})
	if err != nil {
		t.Fatal(err)
	}
	if ev != nil {
		t.Fatal("disabled must return nil engine")
	}
}

func TestInstallSkillEvolution_RequiresSession(t *testing.T) {
	off := false
	cfg := &configs.Config{}
	cfg.SkillEvolution.Enabled = true
	cfg.Session.Enabled = &off
	cfg.Agents.SkillDir = t.TempDir()
	if _, err := installSkillEvolution(cfg, nil, nil, &mockChatCompleter{}); err == nil {
		t.Fatal("expected session.enabled error")
	}
}

func TestInstallSkillEvolution_RequiresSkillDir(t *testing.T) {
	cfg := &configs.Config{}
	cfg.SkillEvolution.Enabled = true
	if _, err := installSkillEvolution(cfg, nil, nil, &mockChatCompleter{}); err == nil {
		t.Fatal("expected skill_dir error")
	}
}

func TestInstallSkillEvolution_RequiresTranscriptStore(t *testing.T) {
	cfg := &configs.Config{}
	cfg.SkillEvolution.Enabled = true
	cfg.Agents.SkillDir = t.TempDir()
	stack := registries.LoadSkillStack(context.Background(), "", nil)
	if _, err := installSkillEvolution(cfg, &Options{}, stack, &mockChatCompleter{}); err == nil {
		t.Fatal("expected transcript store error")
	}
}

func TestNew_SkillEvolveWhenEnabled(t *testing.T) {
	skillDir := t.TempDir()
	store, err := sessionlogs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10, SkillDir: skillDir},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}
	cfg.SkillEvolution.Enabled = true

	result, err := New(cfg, &mockChatCompleter{}, nil, nil, &Options{IterationStore: store})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if result.SkillEvolve == nil {
		t.Fatal("expected SkillEvolve")
	}
	if _, err := result.SkillEvolve.Queue.Put(skillevolve.Proposal{
		Status:    skillevolve.StatusPending,
		Extracted: skillevolve.ExtractedSkill{Name: "probe"},
	}); err != nil {
		t.Fatalf("Put proposal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, ".proposals")); err != nil {
		t.Fatalf(".proposals missing: %v", err)
	}
}

func TestNew_SkillEvolveDisabledNil(t *testing.T) {
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}
	result, err := New(cfg, &mockChatCompleter{}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.SkillEvolve != nil {
		t.Fatal("disabled SkillEvolve must be nil")
	}
}
