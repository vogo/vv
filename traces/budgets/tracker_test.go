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

package budgets

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewSessionDisabledWhenZero(t *testing.T) {
	if got := NewSession(Config{}); got != nil {
		t.Fatalf("NewSession with zero config: want nil, got %#v", got)
	}
}

func TestNewDailyDisabledWhenZero(t *testing.T) {
	if got := NewDaily(Config{}); got != nil {
		t.Fatalf("NewDaily with zero config: want nil, got %#v", got)
	}
}

func TestNewSessionUsesDefaultWarn(t *testing.T) {
	s := NewSession(Config{HardTokens: 100})
	if s == nil {
		t.Fatal("NewSession returned nil for enabled config")
	}
	if snap := s.Snapshot(); snap.WarnPercent != DefaultWarnPercent {
		t.Fatalf("WarnPercent: want %v, got %v", DefaultWarnPercent, snap.WarnPercent)
	}
}

func TestAddAccumulatesAndReportsExceeded(t *testing.T) {
	s := NewSession(Config{HardTokens: 100})

	res := s.Add(40, 0)
	if res.Exceeded != nil {
		t.Fatalf("Add(40) should not be exceeded, got %v", res.Exceeded)
	}
	if snap := s.Snapshot(); snap.UsedTokens != 40 {
		t.Fatalf("UsedTokens after 40: want 40, got %d", snap.UsedTokens)
	}

	// Push to 100 (at limit): exceeded should fire.
	res = s.Add(60, 0)
	if res.Exceeded == nil {
		t.Fatal("Add pushing to limit should set Exceeded")
	}
	if res.Exceeded.Scope != ScopeSession || res.Exceeded.Dimension != "tokens" {
		t.Fatalf("Exceeded fields: scope=%q dim=%q", res.Exceeded.Scope, res.Exceeded.Dimension)
	}
}

func TestCheckGatesAfterExceed(t *testing.T) {
	s := NewSession(Config{HardTokens: 50})
	s.Add(50, 0) // at limit

	err := s.Check()
	if err == nil {
		t.Fatal("Check after reaching limit should return non-nil error")
	}
	if err.Scope != ScopeSession || err.Used != 50 || err.Limit != 50 {
		t.Fatalf("Check error: %#v", err)
	}
}

func TestWarnFiresOnceAndOnlyOnCrossing(t *testing.T) {
	// warn at 0.5, limit 1000 -> warn crosses at 500.
	s := NewSession(Config{HardTokens: 1000, WarnPercent: 0.5})

	if res := s.Add(400, 0); res.WarnCrossed {
		t.Fatal("warn should not fire below threshold")
	}
	if res := s.Add(110, 0); !res.WarnCrossed { // now at 510, above 500
		t.Fatal("warn should fire when crossing threshold")
	}
	if res := s.Add(50, 0); res.WarnCrossed {
		t.Fatal("warn should fire only once")
	}
}

func TestCostDimensionWarnAndExceed(t *testing.T) {
	s := NewSession(Config{HardCostUSD: 10, WarnPercent: 0.5})

	if res := s.Add(0, 4.0); res.WarnCrossed {
		t.Fatal("cost warn should not fire below threshold")
	}
	if res := s.Add(0, 2.0); !res.WarnCrossed || res.Dimension != "cost" {
		t.Fatalf("cost warn should fire at threshold, got %#v", res)
	}
	// Push past limit.
	res := s.Add(0, 4.5)
	if res.Exceeded == nil || res.Exceeded.Dimension != "cost" {
		t.Fatalf("Exceeded should report cost dimension, got %#v", res.Exceeded)
	}
}

func TestTokensAndCostBothConfiguredFirstHitWins(t *testing.T) {
	// tokens=1000 big headroom, cost=1.0 tight.
	s := NewSession(Config{HardTokens: 1000, HardCostUSD: 1.0})

	res := s.Add(50, 1.5)
	if res.Exceeded == nil {
		t.Fatal("Exceeded should be set when cost limit hit")
	}
	if res.Exceeded.Dimension != "cost" {
		t.Fatalf("first-to-hit should be cost, got dimension=%q", res.Exceeded.Dimension)
	}
}

func TestSnapshotRemainingUnlimitedAsNegative(t *testing.T) {
	s := NewSession(Config{HardTokens: 100})
	snap := s.Snapshot()
	if snap.RemainingCostUSD != -1 {
		t.Fatalf("Unlimited cost remaining should be -1, got %v", snap.RemainingCostUSD)
	}
	if snap.RemainingTokens != 100 {
		t.Fatalf("Remaining tokens at start: want 100, got %d", snap.RemainingTokens)
	}

	s.Add(120, 0)
	snap = s.Snapshot()
	if snap.RemainingTokens != 0 {
		t.Fatalf("Remaining tokens after exceed: want 0, got %d", snap.RemainingTokens)
	}
}

func TestNilTrackerIsNoOp(t *testing.T) {
	var t0 *Tracker
	if err := t0.Check(); err != nil {
		t.Fatalf("nil.Check should return nil, got %v", err)
	}
	if res := t0.Add(100, 1.0); res != (AddResult{}) {
		t.Fatalf("nil.Add should return zero result, got %#v", res)
	}
	if snap := t0.Snapshot(); snap != (Snapshot{}) {
		t.Fatalf("nil.Snapshot should return zero value")
	}
	if s := t0.Scope(); s != "" {
		t.Fatalf("nil.Scope should return empty string, got %q", s)
	}
}

func TestConcurrentAddExactTotals(t *testing.T) {
	s := NewSession(Config{HardTokens: 1_000_000})

	var wg sync.WaitGroup
	var warns atomic.Int64
	const goroutines, perG = 50, 200

	for range goroutines {
		wg.Go(func() {
			for range perG {
				if res := s.Add(1, 0); res.WarnCrossed {
					warns.Add(1)
				}
			}
		})
	}
	wg.Wait()

	snap := s.Snapshot()
	if snap.UsedTokens != int64(goroutines*perG) {
		t.Fatalf("concurrent add total: want %d got %d", goroutines*perG, snap.UsedTokens)
	}
	// warnFired is single-shot across all goroutines.
	if warns.Load() > 1 {
		t.Fatalf("warn must fire at most once under concurrency, fired %d times", warns.Load())
	}
}

// fakeClock returns a monotonic clock whose value can be bumped by the test.
type fakeClock struct {
	mu  sync.Mutex
	cur time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

func (c *fakeClock) set(v time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cur = v
}

func TestDailyRollsAtUTCMidnight(t *testing.T) {
	start := time.Date(2026, time.April, 22, 10, 0, 0, 0, time.UTC)
	clk := &fakeClock{cur: start}

	d := NewDailyWithClock(Config{HardTokens: 1000, WarnPercent: 0.5}, clk.now)
	if d == nil {
		t.Fatal("expected non-nil daily tracker")
	}

	// Accumulate to 600 and fire the warn.
	if res := d.Add(600, 0); !res.WarnCrossed {
		t.Fatal("warn should fire at 60% of 1000")
	}

	// Advance past UTC midnight — window rolls, counters + warnFired reset.
	clk.set(time.Date(2026, time.April, 23, 0, 0, 1, 0, time.UTC))

	snap := d.Snapshot()
	if snap.UsedTokens != 0 {
		t.Fatalf("daily roll: tokens should reset to 0, got %d", snap.UsedTokens)
	}

	// New warn should be possible in the new window.
	if res := d.Add(600, 0); !res.WarnCrossed {
		t.Fatal("warn should re-fire in a fresh window")
	}
}

func TestDailyWindowStartAlignsToUTCMidnight(t *testing.T) {
	clk := &fakeClock{cur: time.Date(2026, time.April, 22, 13, 45, 0, 0, time.UTC)}
	d := NewDailyWithClock(Config{HardTokens: 100}, clk.now)
	if d == nil {
		t.Fatal("expected non-nil tracker")
	}
	want := time.Date(2026, time.April, 22, 0, 0, 0, 0, time.UTC)
	if got := d.Snapshot().WindowStart; !got.Equal(want) {
		t.Fatalf("WindowStart: want %v got %v", want, got)
	}
}

func TestSessionScopeLabel(t *testing.T) {
	s := NewSession(Config{HardTokens: 10})
	if s.Scope() != ScopeSession {
		t.Fatalf("scope: want %q got %q", ScopeSession, s.Scope())
	}
	d := NewDaily(Config{HardTokens: 10})
	if d.Scope() != ScopeDaily {
		t.Fatalf("scope: want %q got %q", ScopeDaily, d.Scope())
	}
}
