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
	"testing"

	"github.com/vogo/vage/hook"
	"github.com/vogo/vage/schema"
)

// TestSessionEventCounter_OnEventCounts checks the basic increment path —
// AgentEnd events with a sessionID bump the counter.
func TestSessionEventCounter_OnEventCounts(t *testing.T) {
	c := newSessionEventCounter()
	h := c.Hook()
	for range 3 {
		_ = h.OnEvent(context.Background(), schema.NewEvent(
			schema.EventAgentEnd, "agent", "s1", schema.AgentEndData{},
		))
	}
	if got := c.Count("s1"); got != 3 {
		t.Errorf("Count(s1) = %d, want 3", got)
	}
	if got := c.Count("missing"); got != 0 {
		t.Errorf("Count(missing) = %d, want 0", got)
	}
}

// TestSessionEventCounter_EmptySessionIDIgnored prevents the counter from
// accumulating events that have no session attribution (e.g., framework-
// level startup events).
func TestSessionEventCounter_EmptySessionIDIgnored(t *testing.T) {
	c := newSessionEventCounter()
	h := c.Hook()
	_ = h.OnEvent(context.Background(), schema.NewEvent(
		schema.EventAgentEnd, "agent", "", schema.AgentEndData{},
	))
	if got := c.Count(""); got != 0 {
		t.Errorf("Count('') = %d, want 0", got)
	}
}

// TestSessionEventCounter_FilterScopesToAgentEnd asserts the hook only
// asks for AgentEnd, so the manager won't deliver intermediate events.
func TestSessionEventCounter_FilterScopesToAgentEnd(t *testing.T) {
	c := newSessionEventCounter()
	got := c.Hook().Filter()
	if len(got) != 1 || got[0] != schema.EventAgentEnd {
		t.Errorf("Filter = %v, want [%q]", got, schema.EventAgentEnd)
	}
}

// TestSessionEventCounter_PredicateThresholdZero is the always-on path: a
// non-positive threshold returns a predicate that's permanently true so
// callers can plumb the option transparently when auto-enable is disabled.
func TestSessionEventCounter_PredicateThresholdZero(t *testing.T) {
	c := newSessionEventCounter()
	p := c.Predicate(0)
	if !p(context.Background(), "anything") {
		t.Errorf("predicate with threshold 0 returned false")
	}
}

// TestSessionEventCounter_PredicateGatesUntilThreshold walks the canonical
// flow: predicate stays false while count < threshold, then flips true
// once the threshold is met. Uses the Hook directly to feed events so the
// test doubles as a contract for the production wiring.
func TestSessionEventCounter_PredicateGatesUntilThreshold(t *testing.T) {
	c := newSessionEventCounter()
	pred := c.Predicate(3)
	h := c.Hook()
	ctx := context.Background()

	if pred(ctx, "s1") {
		t.Errorf("predicate returned true at count=0")
	}
	for range 2 {
		_ = h.OnEvent(ctx, schema.NewEvent(schema.EventAgentEnd, "a", "s1", schema.AgentEndData{}))
	}
	if pred(ctx, "s1") {
		t.Errorf("predicate returned true at count=2 (threshold 3)")
	}
	_ = h.OnEvent(ctx, schema.NewEvent(schema.EventAgentEnd, "a", "s1", schema.AgentEndData{}))
	if !pred(ctx, "s1") {
		t.Errorf("predicate returned false at count=3 (threshold 3)")
	}
}

// TestSessionEventCounter_HookManagerIntegration confirms registration
// against a real hook.Manager works end-to-end. This is the wiring
// Init does in production.
func TestSessionEventCounter_HookManagerIntegration(t *testing.T) {
	c := newSessionEventCounter()
	mgr := hook.NewManager()
	mgr.Register(c.Hook())

	ctx := context.Background()
	mgr.Dispatch(ctx, schema.NewEvent(schema.EventAgentEnd, "a", "s1", schema.AgentEndData{}))
	mgr.Dispatch(ctx, schema.NewEvent(schema.EventAgentEnd, "a", "s2", schema.AgentEndData{}))
	mgr.Dispatch(ctx, schema.NewEvent(schema.EventAgentEnd, "a", "s1", schema.AgentEndData{}))

	if got := c.Count("s1"); got != 2 {
		t.Errorf("Count(s1) = %d, want 2", got)
	}
	if got := c.Count("s2"); got != 1 {
		t.Errorf("Count(s2) = %d, want 1", got)
	}
}
