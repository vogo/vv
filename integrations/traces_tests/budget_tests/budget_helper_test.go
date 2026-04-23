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

package budget_tests

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/largemodel"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/traces/budgets"
	"github.com/vogo/vv/traces/costtraces"
)

// stubCompleter is a deterministic aimodel.ChatCompleter that returns a
// fixed usage profile on every call and increments an atomic counter so
// tests can assert that the middleware never reached the (simulated)
// network after a hard-limit rejection.
type stubCompleter struct {
	calls atomic.Int64
	usage aimodel.Usage
}

func (s *stubCompleter) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	s.calls.Add(1)
	return &aimodel.ChatResponse{ID: "stub", Usage: s.usage}, nil
}

func (s *stubCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	s.calls.Add(1)
	// Streaming path is not exercised here; return nil (WrapStream-safe).
	return nil, nil
}

// newCollectingDispatcher returns a thread-safe dispatcher that captures
// every schema.Event it receives plus a getter for the captured slice.
func newCollectingDispatcher() (largemodel.DispatchFunc, func() []schema.Event) {
	var (
		mu     sync.Mutex
		events []schema.Event
	)

	d := largemodel.DispatchFunc(func(_ context.Context, e schema.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	})

	return d, func() []schema.Event {
		mu.Lock()
		defer mu.Unlock()
		// Return a copy so callers iterating the slice can't race with
		// in-flight dispatches from late goroutines.
		out := make([]schema.Event, len(events))
		copy(out, events)

		return out
	}
}

// wrap builds the full middleware stack for a given session/daily
// tracker pair using the supplied pricing and dispatcher. Mirrors how
// setup.Init wires budgets in production.
func wrap(
	t *testing.T,
	session, daily *budgets.Tracker,
	pricing *costtraces.Pricing,
	dispatch largemodel.DispatchFunc,
	base aimodel.ChatCompleter,
) aimodel.ChatCompleter {
	t.Helper()

	var disp budgets.Dispatcher
	if dispatch != nil {
		disp = budgets.Dispatcher(dispatch)
	}

	preCheck, postRecord := budgets.Wire(session, daily, pricing, disp)
	if preCheck == nil && postRecord == nil {
		// Mirrors the real wiring: no trackers → caller skips the middleware.
		return base
	}

	return largemodel.NewBudgetMiddleware(preCheck, postRecord).Wrap(base)
}
