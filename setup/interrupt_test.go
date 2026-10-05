package setup

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

func TestApplyInterrupt_UsesCapabilityNotName(t *testing.T) {
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
	opts.ApplyInterrupt(&full, registries.ProfileFull)
	if full.InterruptStore == nil || full.InterruptPolicy == nil {
		t.Fatal("ProfileFull must receive interrupt wiring")
	}

	var namedFull registries.FactoryOptions
	opts.ApplyInterrupt(&namedFull, registries.ToolProfile{Name: "full"})
	if namedFull.InterruptStore != nil || namedFull.InterruptPolicy != nil {
		t.Fatal("a profile named full without CapInterrupt must not receive the store")
	}

	var custom registries.FactoryOptions
	opts.ApplyInterrupt(&custom, registries.ToolProfile{Name: "custom", Capabilities: []registries.ToolCapability{registries.CapInterrupt}})
	if custom.InterruptStore == nil {
		t.Fatal("a custom profile with CapInterrupt must receive the store")
	}

	var ro registries.FactoryOptions
	opts.ApplyInterrupt(&ro, registries.ProfileReadOnly)
	if ro.InterruptStore != nil || ro.InterruptPolicy != nil {
		t.Fatal("ProfileReadOnly must not receive interrupt wiring")
	}

	var review registries.FactoryOptions
	opts.ApplyInterrupt(&review, registries.ProfileReview)
	if review.InterruptStore != nil || review.InterruptPolicy != nil {
		t.Fatal("ProfileReview must not receive interrupt wiring")
	}

	var primary registries.FactoryOptions
	opts.ApplyInterrupt(&primary, primaryToolProfile(cfg))
	if primary.InterruptStore == nil || primary.InterruptPolicy == nil {
		t.Fatal("primaryToolProfile must receive interrupt wiring")
	}
}

func TestApplyInterrupt_NilOptions(t *testing.T) {
	var opts *Options
	var fo registries.FactoryOptions
	opts.ApplyInterrupt(&fo, registries.ProfileFull)
	if fo.InterruptStore != nil {
		t.Fatal("nil Options must not panic or write a store")
	}
	store, policy, ttl := opts.Interrupt()
	if store != nil || policy != nil || ttl != 0 {
		t.Fatal("nil Options.Interrupt must be zero")
	}

	got := applyInterruptOpts(nil, nil, nil, 0)
	if got != nil {
		t.Fatal("applyInterruptOpts(nil, nil, nil) must stay nil")
	}
}

func TestInstallInterrupt_AuditsUnknownVersionWithoutFailing(t *testing.T) {
	root := t.TempDir()
	cfg := &configs.Config{}
	cfg.Agents.InterruptEnabled = true
	cfg.Session.Dir = root
	cfg.Tools.BashWorkingDir = t.TempDir()

	dir := filepath.Join(sessionRootDir(cfg), "interrupts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old.json"), []byte(`{"version":1,"id":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old.lock"), []byte("lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "partial.json.tmp"), []byte(`{"version":9}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	store, policy, _, err := installInterrupt(cfg, nil)
	if err != nil {
		t.Fatalf("installInterrupt: %v", err)
	}
	if store == nil || policy == nil {
		t.Fatal("unknown version must not fail startup")
	}
	log := buf.String()
	if !strings.Contains(log, "old") {
		t.Fatalf("audit log = %q, want the v1 id", log)
	}
	if strings.Contains(log, ".lock") || strings.Contains(log, ".tmp") {
		t.Fatalf("audit log counted a lock or tmp file: %s", log)
	}
}
