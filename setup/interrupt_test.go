package setup

import (
	"path/filepath"
	"testing"

	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/registries"
)

func TestInstallInterrupt_DisabledIsNoop(t *testing.T) {
	store, policy, ttl, err := installInterrupt(&configs.Config{}, nil)
	if err != nil {
		t.Fatalf("installInterrupt: %v", err)
	}
	if store != nil || policy != nil || ttl != 0 {
		t.Fatalf("disabled interrupt must be zero-cost, got store=%v policy=%v ttl=%s", store, policy, ttl)
	}
}

func TestInstallInterrupt_RequiresSession(t *testing.T) {
	off := false
	cfg := &configs.Config{}
	cfg.Agents.InterruptEnabled = true
	cfg.Session.Enabled = &off

	_, _, _, err := installInterrupt(cfg, nil)
	if err == nil {
		t.Fatal("expected error when session is disabled")
	}
}

func TestInstallInterrupt_RequiresBashRules(t *testing.T) {
	off := false
	cfg := &configs.Config{}
	cfg.Agents.InterruptEnabled = true
	cfg.Tools.BashRules.Enabled = &off
	cfg.Session.Dir = t.TempDir()
	cfg.Tools.BashWorkingDir = t.TempDir()

	_, _, _, err := installInterrupt(cfg, nil)
	if err == nil {
		t.Fatal("expected error when bash_rules is disabled")
	}
}

func TestInstallInterrupt_CreatesFileStore(t *testing.T) {
	cfg := &configs.Config{}
	cfg.Agents.InterruptEnabled = true
	cfg.Agents.AskUserTimeout = 120
	cfg.Session.Dir = t.TempDir()
	cfg.Tools.BashWorkingDir = t.TempDir()

	store, policy, ttl, err := installInterrupt(cfg, nil)
	if err != nil {
		t.Fatalf("installInterrupt: %v", err)
	}
	if store == nil || policy == nil {
		t.Fatal("expected store and policy")
	}
	if ttl.Seconds() != 120 {
		t.Errorf("ttl = %s, want 120s from ask_user_timeout", ttl)
	}

	fs, ok := store.(interface{ Root() string })
	if !ok {
		t.Fatal("expected FileStore with Root()")
	}
	want := filepath.Join(sessionRootDir(cfg), "interrupts")
	if fs.Root() != want {
		t.Errorf("root = %q, want %q", fs.Root(), want)
	}
}

func TestApplyFactoryInterrupt_OnlyFullProfile(t *testing.T) {
	cfg := &configs.Config{}
	cfg.Agents.InterruptEnabled = true
	cfg.Session.Dir = t.TempDir()
	cfg.Tools.BashWorkingDir = t.TempDir()

	store, policy, ttl, err := installInterrupt(cfg, nil)
	if err != nil {
		t.Fatalf("installInterrupt: %v", err)
	}
	opts := applyInterruptOpts(nil, store, policy, ttl)

	var full registries.FactoryOptions
	applyFactoryInterrupt(&full, opts, registries.ProfileFull.Name)
	if full.InterruptStore == nil || full.InterruptPolicy == nil {
		t.Fatal("ProfileFull must receive interrupt wiring")
	}

	var ro registries.FactoryOptions
	applyFactoryInterrupt(&ro, opts, registries.ProfileReadOnly.Name)
	if ro.InterruptStore != nil || ro.InterruptPolicy != nil {
		t.Fatal("non-Full profiles must not receive interrupt wiring")
	}
}
