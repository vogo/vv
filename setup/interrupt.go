package setup

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/interrupt"
	"github.com/vogo/vage/tool/bash"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/dispatches"
	"github.com/vogo/vv/registries"
)

// installInterrupt constructs the FileStore + Dangerous-bash policy pair
// when agents.interrupt_enabled is set. Both-or-neither: a disabled flag
// returns nils (zero-cost); a missing session subsystem or a disabled
// bash classifier is a startup error because the HTTP hard-reject would
// otherwise be skipped on resume with no gate to freeze the batch.
func installInterrupt(cfg *configs.Config, guardian *bash.PathGuardian) (interrupt.Store, taskagent.InterruptPolicy, time.Duration, error) {
	if cfg == nil || !cfg.Agents.InterruptEnabled {
		return nil, nil, 0, nil
	}
	if !cfg.Session.IsEnabled() {
		return nil, nil, 0, fmt.Errorf("agents.interrupt_enabled requires session.enabled")
	}
	if !cfg.Tools.BashRules.IsEnabled() {
		return nil, nil, 0, fmt.Errorf("agents.interrupt_enabled requires tools.bash_rules not disabled")
	}

	var dirs []string
	workdir := ""
	guardianOn := guardian != nil
	if guardianOn && cfg.Tools.AllowedDirs != nil {
		dirs = *cfg.Tools.AllowedDirs
		workdir = cfg.Tools.BashWorkingDir
	}
	classifier, fingerprint := configs.BuildBashClassifierWithFingerprint(cfg.Tools.BashRules, dirs, workdir, guardianOn)
	if classifier == nil {
		return nil, nil, 0, fmt.Errorf("agents.interrupt_enabled: bash classifier is nil")
	}

	root := filepath.Join(sessionRootDir(cfg), "interrupts")
	store, err := interrupt.NewFileStore(root)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("interrupt store: %w", err)
	}
	auditInterruptVersions(store, root)

	ttl := time.Duration(cfg.Agents.EffectiveInterruptLeaseTTL()) * time.Second
	policy := dispatches.NewDangerousBashPolicy(classifier, guardian, fingerprint)
	slog.Info("vv: interrupt store enabled", "dir", root, "lease_ttl", ttl)
	return store, policy, ttl, nil
}

// auditInterruptVersions logs unreadable interrupt files. It does not
// delete them and does not fail startup: Get still returns
// ErrUnknownVersion for those ids. The operator has to move or delete
// the files under the interrupts directory before those records can resume.
func auditInterruptVersions(store *interrupt.FileStore, root string) {
	audit, err := store.AuditVersions(context.Background())
	if err != nil || audit.Unknown > 0 {
		slog.Error(
			"vv: unreadable interrupt records",
			"dir", root,
			"current", audit.Current,
			"legacy", audit.Legacy,
			"unknown", audit.Unknown,
			"unknown_ids", audit.UnknownIDs,
			"error", err,
			"action", "move or delete these files under the interrupts directory before resuming them",
		)
	}
}

// Interrupt returns the durable HITL triple. A nil Options returns zeros.
func (o *Options) Interrupt() (store interrupt.Store, policy taskagent.InterruptPolicy, ttl time.Duration) {
	if o == nil {
		return nil, nil, 0
	}
	return o.InterruptStore, o.InterruptPolicy, o.InterruptLeaseTTL
}

// ApplyInterrupt writes the HITL triple onto fo when the profile declares
// CapInterrupt. A nil receiver, a nil fo, or a profile without the
// capability leaves fo unchanged. This is not a tool registration, and
// worker assembly must not call it.
func (o *Options) ApplyInterrupt(fo *registries.FactoryOptions, profile registries.ToolProfile) {
	if o == nil || fo == nil || !profile.Has(registries.CapInterrupt) {
		return
	}
	store, policy, ttl := o.Interrupt()
	fo.InterruptStore = store
	fo.InterruptPolicy = policy
	fo.InterruptLeaseTTL = ttl
}

func applyInterruptOpts(opts *Options, store interrupt.Store, policy taskagent.InterruptPolicy, ttl time.Duration) *Options {
	if store == nil && policy == nil {
		return opts
	}
	if opts == nil {
		opts = &Options{}
	}
	opts.InterruptStore = store
	opts.InterruptPolicy = policy
	opts.InterruptLeaseTTL = ttl
	return opts
}

func interruptStoreFrom(r *Result) interrupt.Store {
	if r == nil {
		return nil
	}
	return r.InterruptStore
}
