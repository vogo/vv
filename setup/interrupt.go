package setup

import (
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

	classifier := configs.BuildBashClassifier(cfg.Tools.BashRules)
	if classifier == nil {
		return nil, nil, 0, fmt.Errorf("agents.interrupt_enabled: bash classifier is nil")
	}

	root := filepath.Join(sessionRootDir(cfg), "interrupts")
	store, err := interrupt.NewFileStore(root)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("interrupt store: %w", err)
	}

	ttl := time.Duration(cfg.Agents.EffectiveInterruptLeaseTTL()) * time.Second
	policy := dispatches.NewDangerousBashPolicy(classifier, guardian)
	slog.Info("vv: interrupt store enabled", "dir", root, "lease_ttl", ttl)
	return store, policy, ttl, nil
}

func getInterruptStore(opts *Options) interrupt.Store {
	if opts == nil {
		return nil
	}
	return opts.InterruptStore
}

func getInterruptPolicy(opts *Options) taskagent.InterruptPolicy {
	if opts == nil {
		return nil
	}
	return opts.InterruptPolicy
}

func getInterruptLeaseTTL(opts *Options) time.Duration {
	if opts == nil {
		return 0
	}
	return opts.InterruptLeaseTTL
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

func applyFactoryInterrupt(fo *registries.FactoryOptions, opts *Options, profileName string) {
	if fo == nil || profileName != registries.ProfileFull.Name {
		return
	}
	fo.InterruptStore = getInterruptStore(opts)
	fo.InterruptPolicy = getInterruptPolicy(opts)
	fo.InterruptLeaseTTL = getInterruptLeaseTTL(opts)
}

func interruptStoreFrom(r *Result) interrupt.Store {
	if r == nil {
		return nil
	}
	return r.InterruptStore
}
