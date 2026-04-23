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

package tracelog_tests

import (
	"context"
	"os"
	"testing"

	"github.com/vogo/vage/schema"
)

// TestIntegration_Disabled_NoHookNoFiles (US-2)
// Scenario: cfg.Trace.Enabled is unset (nil, i.e. the default). After
// setup.Init, the public handle to the hook manager must be nil — no
// AsyncHook is installed, no consumer goroutine is running, and no files
// are created under the would-be trace directory even after a full agent
// run. This is the US-2 hard guardrail: "zero measurable cost when
// disabled."
func TestIntegration_Disabled_NoHookNoFiles(t *testing.T) {
	traceDir := t.TempDir()
	// enabled=false → configs.TraceConfig.Enabled stays nil per makeTraceConfig.
	cfg := makeTraceConfig(t, traceDir, false)

	// Precondition: ensure we are asserting the default path, not an
	// accidental enabled one.
	if cfg.Trace.IsEnabled() {
		t.Fatal("test setup regression: disabled config reports IsEnabled()=true")
	}

	initResult, a, _ := initWithStubAgent(t, cfg, "doesnt matter")

	// US-2 core invariant: no HookManager when trace is off.
	if initResult.SetupResult.HookManager != nil {
		t.Fatal("US-2: SetupResult.HookManager must be nil when Trace.Enabled is false")
	}

	// US-2 / US-4: Shutdown is still exposed so main.go can defer it
	// unconditionally — but must be a safe no-op.
	if initResult.Shutdown == nil {
		t.Fatal("US-2: InitResult.Shutdown must always be non-nil (no-op acceptable)")
	}

	// Calling Shutdown with a cancellable context must not panic.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already-cancelled context to exercise the no-op path.
	initResult.Shutdown(ctx)

	// Agent runs still succeed — they just don't emit events anywhere.
	req := &schema.RunRequest{
		SessionID: "disabled-session",
		Messages:  []schema.Message{schema.NewUserMessage("hi")},
	}
	if _, err := a.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// No files should materialise under the would-be project trace dir.
	projectDir := projectTraceDir(traceDir, cfg.Tools.BashWorkingDir)

	// The entire project dir should be absent (JSONLHook never ran
	// MkdirAll). Allow either "not exist" or an empty directory to be
	// forgiving about on-disk artefacts the rest of vv might seed, but
	// fail if there are files.
	info, err := os.Stat(projectDir)
	switch {
	case os.IsNotExist(err):
		// Perfect — directory was never created.
	case err != nil:
		t.Fatalf("stat %q: %v", projectDir, err)
	case !info.IsDir():
		t.Fatalf("expected %q to be a directory when present, got %v", projectDir, info.Mode())
	default:
		entries, readErr := os.ReadDir(projectDir)
		if readErr != nil {
			t.Fatalf("read project trace dir: %v", readErr)
		}
		if len(entries) > 0 {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("US-2: trace files present even when disabled: %v", names)
		}
	}
}
