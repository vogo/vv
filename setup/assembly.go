/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package setup

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/hook"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/session"
	"github.com/vogo/vage/workspace"
	"github.com/vogo/vv/configs"
)

// cleanup releases the resources a single installer opened. Init invokes it
// with context.Background() while rolling back a failed startup and with the
// caller-supplied context during InitResult.Shutdown, so context-aware
// teardown — chiefly the async hook drain bounded by CONFIG-R12's independent
// Shutdown context — behaves identically on both the failure and success
// paths. A no-op stage returns a nil cleanup.
type cleanup func(context.Context)

// subsystemInstaller assembles one optional subsystem onto opts. It returns
// the cleanup that releases whatever it constructed (nil when the stage is a
// disabled/soft-degraded no-op) and an error. Ownership contract: when an
// installer returns an error it MUST already have released any resource it
// opened during this call, because nothing is on the rollback stack yet;
// once it returns successfully, its resources are owned solely by the
// returned cleanup.
type subsystemInstaller func(cfg *configs.Config, opts *Options) (cleanup, error)

// assembly threads the handles that one installer constructs and a later
// installer consumes (the wrapped LLM, the memory manager, the process hook
// manager, and the session/tree handles) without widening Init's public
// surface: every field here is package-internal scratch state, created per
// Init call and discarded once assembly finishes. Installers are methods on
// *assembly so they share this state through the receiver rather than through
// Options.
type assembly struct {
	wrappedLLM    aimodel.ChatCompleter
	memMgr        *memory.Manager
	persistentMem memory.Memory

	hookManager   *hook.Manager
	sessionStore  session.SessionStore
	planWorkspace workspace.Workspace

	result *Result
}

// runInstallers runs installers in order, pushing each non-nil cleanup onto a
// LIFO stack. On the first error it rolls back every accumulated cleanup in
// strict reverse order (using context.Background(), matching the legacy
// failure path) and returns the original error with a nil stack — a
// half-constructed startup never leaks a running hook or an open store. On
// success it returns the ordered cleanup stack; the caller releases it in
// reverse via shutdownFromCleanups.
func runInstallers(cfg *configs.Config, opts *Options, installers []subsystemInstaller) ([]cleanup, error) {
	var cleanups []cleanup
	for _, install := range installers {
		c, err := install(cfg, opts)
		if err != nil {
			runCleanups(context.Background(), cleanups)
			return nil, err
		}
		if c != nil {
			cleanups = append(cleanups, c)
		}
	}
	return cleanups, nil
}

// runCleanups invokes cleanups in reverse (LIFO) order: the most recently
// constructed resource is released first, so consumers stop before the
// dependencies they drain into (vector hook before vector store, all hooks
// before the persistent memory store).
func runCleanups(ctx context.Context, cleanups []cleanup) {
	for i := len(cleanups) - 1; i >= 0; i-- {
		cleanups[i](ctx)
	}
}

// shutdownFromCleanups turns the ordered cleanup stack into the idempotent
// InitResult.Shutdown. It releases resources in the same reverse order as
// rollback but with the caller-supplied context, so hook drains honour the
// independent Shutdown timeout (CONFIG-R12). sync.Once guards against a
// double Shutdown call double-releasing the stack.
func shutdownFromCleanups(cleanups []cleanup) func(context.Context) {
	var once sync.Once
	return func(ctx context.Context) {
		once.Do(func() { runCleanups(ctx, cleanups) })
	}
}

// ensureHookManager is the single seam that lazily brings up a process
// hook.Manager when trace + session are both off but an optional hook
// (metrics, vector auto-write) still needs a host. Both callers share it, so
// no second `hookManager == nil` bring-up exists anywhere in setup.
//
// When a manager already exists it returns a no-op cleanup — the existing
// manager's stop is owned by installHookSession's cleanup. When this call
// creates the manager it starts it, wires it onto opts, records it on the
// assembly, and returns the cleanup that stops it; the caller is responsible
// for composing that cleanup into what it pushes onto the rollback stack.
func (a *assembly) ensureHookManager(opts *Options) (cleanup, error) {
	if a.hookManager != nil {
		return func(context.Context) {}, nil
	}

	mgr := hook.NewManager()
	if err := mgr.Start(context.Background()); err != nil {
		return nil, err
	}

	a.hookManager = mgr
	opts.HookManager = mgr

	return func(ctx context.Context) {
		if err := mgr.Stop(ctx); err != nil {
			slog.Warn("vv: stop hooks", "error", err)
		}
	}, nil
}

// installMemory opens the persistent-memory store and builds the three-layer
// memory manager. The store is the first resource on the rollback stack, so
// its close runs last during both rollback and Shutdown — after every hook
// has stopped writing to it.
func (a *assembly) installMemory(cfg *configs.Config, _ *Options) (cleanup, error) {
	store, closeStore, err := openMemoryStore(cfg.Memory)
	if err != nil {
		return nil, err
	}

	a.persistentMem = memory.NewPersistentMemoryWithStore(store)
	a.memMgr = memory.NewManager(
		memory.WithStore(a.persistentMem),
		memory.WithPromoter(memory.PromoteAll()),
		memory.WithCompressor(memory.NewSlidingWindowCompressor(cfg.Memory.SessionWindow)),
	)

	return func(context.Context) { closeStore() }, nil
}

// installHookSession constructs the process hook.Manager plus the optional
// trace + session hooks and Plan Workspace. buildHookManagerAndSession
// returns a context-aware shutdown that is a no-op when no manager was built,
// so the cleanup can be pushed unconditionally.
func (a *assembly) installHookSession(cfg *configs.Config, opts *Options) (cleanup, error) {
	hookManager, sessionStore, planWorkspace, hookShutdown, err := buildHookManagerAndSession(cfg)
	if err != nil {
		return nil, fmt.Errorf("setup hooks: %w", err)
	}

	a.hookManager = hookManager
	a.sessionStore = sessionStore
	a.planWorkspace = planWorkspace

	if hookManager != nil {
		opts.HookManager = hookManager
	}
	if planWorkspace != nil {
		opts.Workspace = planWorkspace
	}

	return func(ctx context.Context) { hookShutdown(ctx) }, nil
}

// installIteration wires the per-iteration ReAct checkpoint store. It reuses
// <session-root>/<id>/ so it is a no-op unless the session subsystem is on;
// the store holds no closable resource, so it contributes no cleanup.
func (a *assembly) installIteration(cfg *configs.Config, opts *Options) (cleanup, error) {
	iterStore, err := buildIterationStore(cfg)
	if err != nil {
		return nil, err
	}
	if iterStore == nil {
		return nil, nil
	}

	opts.IterationStore = iterStore
	slog.Info("vv: iteration checkpoint store enabled", "dir", sessionRootDir(cfg))
	return nil, nil
}

// installMetrics wires the P0-5 observability triple's store + hook. The hook
// needs a running manager; if none exists yet (trace + session both off) it
// is brought up through the shared ensureHookManager seam. The metrics store
// holds no closable resource, so the only cleanup is the seam's manager stop
// (a no-op when a manager already existed).
func (a *assembly) installMetrics(cfg *configs.Config, opts *Options) (cleanup, error) {
	metricsStore, err := buildMetricsStore(cfg)
	if err != nil {
		return nil, err
	}
	if metricsStore == nil {
		return nil, nil
	}

	opts.MetricsStore = metricsStore

	metricsHook := buildMetricsHook(cfg, metricsStore)
	opts.MetricsHook = metricsHook
	// Adapt RecordCheckpointFailure to taskagent's callback shape: the
	// metrics layer does not use the underlying save error (slog.Warn already
	// logged it) so we drop it here. Errors from the metrics store itself are
	// logged inside the hook.
	opts.CheckpointFailureCB = func(ctx context.Context, sid string, _ error) {
		_ = metricsHook.RecordCheckpointFailure(ctx, sid)
	}

	seamCleanup, err := a.ensureHookManager(opts)
	if err != nil {
		return nil, fmt.Errorf("start hooks for metrics: %w", err)
	}

	a.hookManager.Register(metricsHook)
	slog.Info("vv: session metrics hook registered", "dir", sessionRootDir(cfg))
	return seamCleanup, nil
}

// installBuildReport wires the per-turn BuildReport archive sink. The sink
// holds no closable resource, so it contributes no cleanup.
func (a *assembly) installBuildReport(cfg *configs.Config, opts *Options) (cleanup, error) {
	sink, err := buildBuildReportSink(cfg)
	if err != nil {
		return nil, err
	}
	if sink == nil {
		return nil, nil
	}

	opts.BuildReportSink = sink
	slog.Info("vv: build_report archive enabled", "dir", sessionRootDir(cfg))
	return nil, nil
}

// installTree wires the SessionTree subsystem. It is gated on the session
// subsystem being on (shared root) plus the explicit flag; a bad promoter
// config is fail-fast. The FileTreeStore holds no closable resource, so the
// stage contributes no cleanup — its hook manager stop is owned by
// installHookSession.
func (a *assembly) installTree(cfg *configs.Config, opts *Options) (cleanup, error) {
	if !cfg.SessionTree.IsEnabled() {
		return nil, nil
	}
	if a.sessionStore == nil {
		return nil, fmt.Errorf("session_tree.enabled requires session.enabled (set session.enabled: true)")
	}

	treeStore, err := buildTreeStore(cfg, a.wrappedLLM, a.hookManager)
	if err != nil {
		return nil, fmt.Errorf("session tree: %w", err)
	}
	opts.TreeStore = treeStore

	// Auto-enable gating: wire an in-process AgentEnd-event counter to the
	// same hook.Manager so SessionTreeSource activates per session once the
	// threshold is crossed. hookManager is non-nil here because the session
	// subsystem requires it; the defensive fallback skips the wiring
	// otherwise.
	if cfg.SessionTree.AutoEnableAfterEvents > 0 && a.hookManager != nil {
		counter := newSessionEventCounter()
		a.hookManager.Register(counter.Hook())
		opts.TreePredicate = counter.Predicate(cfg.SessionTree.AutoEnableAfterEvents)
		slog.Info("vv: session tree auto-enable gated",
			"after_events", cfg.SessionTree.AutoEnableAfterEvents)
	}

	slog.Info("vv: session tree enabled",
		"promotion", cfg.SessionTree.Promotion.IsEnabled(),
		"promoter", cfg.SessionTree.Promotion.PromoterKind())
	return nil, nil
}

// installVector wires the vector subsystem (store + embedder + optional
// auto-write hook). It is independent of the session subsystem, so it is the
// one stage that can trigger the ensureHookManager seam in practice. The
// cleanup releases in reverse of construction: stop the auto-write hook, stop
// a seam-created manager, then close the store the hook drains into.
func (a *assembly) installVector(cfg *configs.Config, opts *Options) (cleanup, error) {
	if !cfg.Vector.IsEnabled() {
		return nil, nil
	}

	subsys, err := buildVectorSubsystem(cfg.Vector)
	if err != nil {
		return nil, fmt.Errorf("vector subsystem: %w", err)
	}
	if subsys == nil {
		// Disabled or soft-failed (e.g. missing API key) — a no-op path.
		return nil, nil
	}

	opts.VectorStore = subsys.Store
	opts.VectorEmbedder = subsys.Embedder
	opts.VectorTopK = cfg.Vector.TopK

	// The VectorStore interface has no Close; only backends that hold a
	// connection (or a test double) implement io.Closer. Guard the assertion
	// so in-memory stores are a silent no-op.
	closeStore := func(context.Context) {
		if c, ok := subsys.Store.(io.Closer); ok {
			if cerr := c.Close(); cerr != nil {
				slog.Warn("vv: close vector store", "error", cerr)
			}
		}
	}

	if subsys.Hook == nil {
		return closeStore, nil
	}

	// Auto-write requires a running manager; bring one up if trace + session
	// are both off. On any failure past this point we must roll back this
	// stage's own resources before returning — nothing is on the stack yet.
	seamCleanup, err := a.ensureHookManager(opts)
	if err != nil {
		closeStore(context.Background())
		return nil, fmt.Errorf("start hooks for vector auto-write: %w", err)
	}

	a.hookManager.RegisterAsync(subsys.Hook)
	if startErr := subsys.Hook.Start(context.Background()); startErr != nil {
		seamCleanup(context.Background())
		closeStore(context.Background())
		return nil, fmt.Errorf("start vector auto-write hook: %w", startErr)
	}

	return func(ctx context.Context) {
		// Stop the consumer before closing the store it writes into. Hook.Stop
		// is idempotent (stopOnce), so a later manager.Stop that also stops
		// this hook is a harmless no-op.
		if serr := subsys.Hook.Stop(ctx); serr != nil {
			slog.Warn("vv: stop vector auto-write hook", "error", serr)
		}
		seamCleanup(ctx)
		closeStore(ctx)
	}, nil
}

// installTreeVector installs the vectorhook decorator over opts.TreeStore once
// both Tree and Vector subsystems are active. The decorator owns no new
// resource (it wraps the already-tracked tree + vector handles), so a
// successful wrap contributes no cleanup; a wrap error triggers full rollback
// of every earlier stage.
func (a *assembly) installTreeVector(cfg *configs.Config, opts *Options) (cleanup, error) {
	if err := maybeWrapTreeWithVectorIndex(cfg, opts); err != nil {
		return nil, err
	}
	return nil, nil
}

// installAgents is the final consumer: it builds every agent + the dispatcher
// from the fully-populated Options. It is last, so nothing runs after it and
// it contributes no cleanup; the assembled Result is stashed for InitResult.
func (a *assembly) installAgents(cfg *configs.Config, opts *Options) (cleanup, error) {
	result, err := New(cfg, a.wrappedLLM, a.memMgr, a.persistentMem, opts)
	if err != nil {
		return nil, fmt.Errorf("setup agents: %w", err)
	}

	a.result = result
	return nil, nil
}
