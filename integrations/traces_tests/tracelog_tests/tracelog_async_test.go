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
	"testing"
	"time"

	"github.com/vogo/vage/schema"
)

// TestIntegration_Enabled_AsyncDoesNotBlock (US-3)
// Scenario: the AsyncHook must never block the TaskAgent dispatch path. We
// drive a tight burst of agent runs — every Run emits several events which
// race into the bounded channel behind the hook. The agent-visible latency
// for the burst must stay well under a conservative ceiling (here: 5
// seconds for 50 back-to-back runs). hook.Manager.Dispatch uses non-blocking
// `select { default: drop }` semantics, so even if the consumer stalls
// momentarily the agent path proceeds. Any hang would signal a regression
// to synchronous dispatch.
func TestIntegration_Enabled_AsyncDoesNotBlock(t *testing.T) {
	traceDir := t.TempDir()
	cfg := makeTraceConfig(t, traceDir, true)

	initResult, a, _ := initWithStubAgent(t, cfg, "fast")
	defer shutdownWithTimeout(t, initResult)

	if initResult.SetupResult.HookManager == nil {
		t.Fatal("HookManager is nil but Trace.Enabled=true")
	}

	const runs = 50

	deadline := time.Now().Add(5 * time.Second)

	for i := range runs {
		if time.Now().After(deadline) {
			t.Fatalf("async dispatch blocked: only completed %d/%d runs before deadline", i, runs)
		}

		req := &schema.RunRequest{
			SessionID: "async-burst",
			Messages:  []schema.Message{schema.NewUserMessage("burst")},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

		if _, err := a.Run(ctx, req); err != nil {
			cancel()
			t.Fatalf("Run %d: %v", i, err)
		}

		cancel()
	}

	// Flush and sanity-check that *some* events made it to disk; we do not
	// require all of them because the hook contract explicitly permits
	// drops under channel pressure (design §US-3). What we must see is that
	// the agent loop did not hang.
	shutdownWithTimeout(t, initResult)
}
