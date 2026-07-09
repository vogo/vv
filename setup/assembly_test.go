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
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/vector"
	"github.com/vogo/vv/configs"
)

// recorder captures the order in which installers construct and cleanups
// release, so tests can assert strict reverse (LIFO) rollback.
type recorder struct {
	events []string
}

func (r *recorder) add(s string) { r.events = append(r.events, s) }

// installerStub builds a subsystemInstaller that records its own name on
// construction and pushes a cleanup recording "name-cleanup". When failWith is
// non-nil the installer records "name-fail" and returns the error without a
// cleanup (mirroring an installer that must release its own partial resources
// before returning).
func installerStub(rec *recorder, name string, failWith error) subsystemInstaller {
	return func(_ *configs.Config, _ *Options) (cleanup, error) {
		if failWith != nil {
			rec.add(name + "-fail")
			return nil, failWith
		}
		rec.add(name + "-construct")
		return func(context.Context) { rec.add(name + "-cleanup") }, nil
	}
}

func TestRunInstallers_RollbackReverseOnMidFailure(t *testing.T) {
	rec := &recorder{}
	boom := errors.New("boom")

	cleanups, err := runInstallers(nil, &Options{}, []subsystemInstaller{
		installerStub(rec, "a", nil),
		installerStub(rec, "b", nil),
		installerStub(rec, "c", boom),
		installerStub(rec, "d", nil), // must never run
	})

	if !errors.Is(err, boom) {
		t.Fatalf("want boom error, got %v", err)
	}
	if cleanups != nil {
		t.Fatalf("want nil cleanup stack on failure, got %v", cleanups)
	}

	want := []string{
		"a-construct",
		"b-construct",
		"c-fail",
		// rollback: strict reverse of successful stages, each once.
		"b-cleanup",
		"a-cleanup",
	}
	if strings.Join(rec.events, ",") != strings.Join(want, ",") {
		t.Fatalf("rollback order mismatch:\n got %v\nwant %v", rec.events, want)
	}
}

func TestRunInstallers_SuccessKeepsStackUntilShutdown(t *testing.T) {
	rec := &recorder{}

	cleanups, err := runInstallers(nil, &Options{}, []subsystemInstaller{
		installerStub(rec, "a", nil),
		installerStub(rec, "b", nil),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cleanups) != 2 {
		t.Fatalf("want 2 cleanups, got %d", len(cleanups))
	}
	// Nothing releases until Shutdown fires.
	for _, e := range rec.events {
		if strings.HasSuffix(e, "-cleanup") {
			t.Fatalf("cleanup ran before Shutdown: %v", rec.events)
		}
	}

	shutdown := shutdownFromCleanups(cleanups)
	shutdown(context.Background())
	shutdown(context.Background()) // idempotent — must not double-release

	want := []string{"a-construct", "b-construct", "b-cleanup", "a-cleanup"}
	if strings.Join(rec.events, ",") != strings.Join(want, ",") {
		t.Fatalf("shutdown order mismatch:\n got %v\nwant %v", rec.events, want)
	}
}

// countingVectorStore is a vector.VectorStore that additionally implements
// io.Closer and counts Close calls — the fake used to assert the vector
// installer's rollback closes the store exactly once.
type countingVectorStore struct{ closed int }

func (c *countingVectorStore) Add(context.Context, vector.Document) error { return nil }
func (c *countingVectorStore) Search(context.Context, []float32, vector.SearchOptions) ([]vector.SearchHit, error) {
	return nil, nil
}
func (c *countingVectorStore) Delete(context.Context, string) error            { return nil }
func (c *countingVectorStore) List(context.Context) ([]vector.Document, error) { return nil, nil }
func (c *countingVectorStore) Close() error                                    { c.closed++; return nil }

// countingAsyncHook is an AsyncHook whose Stop count proves the process hook
// manager was stopped during rollback.
type countingAsyncHook struct {
	ch      chan schema.Event
	stopped int
}

func (h *countingAsyncHook) EventChan() chan<- schema.Event { return h.ch }
func (h *countingAsyncHook) Filter() []string               { return nil }
func (h *countingAsyncHook) Start(context.Context) error    { return nil }
func (h *countingAsyncHook) Stop(context.Context) error     { h.stopped++; return nil }

// TestRunInstallers_VectorStoreClosedOnPostVectorFailure injects a failure
// after the vector store is constructed but before the tree↔vector wrap — the
// exact gap the legacy Init leaked. It asserts that the vector store's Close,
// the tree resource release, and the hook manager Stop each fire exactly once,
// in strict reverse order, and that the original error is preserved.
func TestRunInstallers_VectorStoreClosedOnPostVectorFailure(t *testing.T) {
	rec := &recorder{}
	a := &assembly{}
	store := &countingVectorStore{}
	countingHook := &countingAsyncHook{ch: make(chan schema.Event, 1)}
	wrapErr := errors.New("tree vector wrap failed")

	// hook/session stage: bring up a real manager, register a counting async
	// hook so manager.Stop is observable, and hand its stop back as cleanup.
	installHook := func(_ *configs.Config, opts *Options) (cleanup, error) {
		c, err := a.ensureHookManager(opts)
		if err != nil {
			return nil, err
		}
		a.hookManager.RegisterAsync(countingHook)
		if startErr := a.hookManager.Start(context.Background()); startErr != nil {
			return nil, startErr
		}
		return func(ctx context.Context) {
			rec.add("hook-stop")
			c(ctx)
		}, nil
	}

	// tree stage: a resource with a counted release.
	installTree := func(_ *configs.Config, _ *Options) (cleanup, error) {
		return func(context.Context) { rec.add("tree-release") }, nil
	}

	// vector stage: construct the counting store and return the same
	// io.Closer-based cleanup shape the production installVector uses.
	installVec := func(_ *configs.Config, opts *Options) (cleanup, error) {
		opts.VectorStore = store
		return func(context.Context) {
			rec.add("vector-close")
			if c, ok := opts.VectorStore.(interface{ Close() error }); ok {
				_ = c.Close()
			}
		}, nil
	}

	// tree↔vector wrap stage: fails, triggering full rollback.
	installWrap := func(_ *configs.Config, _ *Options) (cleanup, error) {
		return nil, wrapErr
	}

	cleanups, err := runInstallers(nil, &Options{}, []subsystemInstaller{
		installHook,
		installTree,
		installVec,
		installWrap,
	})

	if !errors.Is(err, wrapErr) {
		t.Fatalf("want original wrap error preserved, got %v", err)
	}
	if cleanups != nil {
		t.Fatalf("want nil cleanup stack after rollback, got %v", cleanups)
	}
	if store.closed != 1 {
		t.Fatalf("vector store Close count = %d, want 1", store.closed)
	}
	if countingHook.stopped != 1 {
		t.Fatalf("hook manager Stop count = %d, want 1", countingHook.stopped)
	}

	// Reverse order: vector store first, then tree, then the hook manager —
	// consumers stop before the resources they drain into.
	want := []string{"vector-close", "tree-release", "hook-stop"}
	if strings.Join(rec.events, ",") != strings.Join(want, ",") {
		t.Fatalf("rollback order mismatch:\n got %v\nwant %v", rec.events, want)
	}
}

// TestEnsureHookManager_SharedLazySeam exercises the one lazy-start seam that
// both the metrics and vector auto-write installers depend on: it creates and
// starts a manager on first need and is a no-op once one exists.
func TestEnsureHookManager_SharedLazySeam(t *testing.T) {
	a := &assembly{}
	opts := &Options{}

	// First call (trace + session both off) brings up a manager.
	c1, err := a.ensureHookManager(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.hookManager == nil {
		t.Fatal("expected a manager to be created")
	}
	if opts.HookManager != a.hookManager {
		t.Fatal("expected opts.HookManager wired to the created manager")
	}
	created := a.hookManager

	// Second call (a manager already exists) must not replace it and returns a
	// no-op cleanup — proving both consumers share the same seam.
	c2, err := a.ensureHookManager(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.hookManager != created {
		t.Fatal("second ensureHookManager replaced the existing manager")
	}

	// Both cleanups are safe to run.
	c2(context.Background())
	c1(context.Background())
}

// TestSetupHasSingleLazyHookManagerSeam guards acceptance criterion #2: the
// "bring a hook manager up on demand" logic must live in exactly one place.
// buildHookManagerAndSession creates a manager unconditionally for trace /
// session; ensureHookManager is the sole lazy seam. Any third hook.NewManager
// in production source signals a reintroduced duplicate bring-up.
func TestSetupHasSingleLazyHookManagerSeam(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read setup dir: %v", err)
	}

	total := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		total += strings.Count(string(data), "hook.NewManager()")
	}

	// Exactly two: buildHookManagerAndSession + ensureHookManager.
	if total != 2 {
		t.Fatalf("hook.NewManager() appears %d times in setup source, want 2 "+
			"(buildHookManagerAndSession + the single ensureHookManager seam)", total)
	}
}
