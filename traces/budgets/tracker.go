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

// Package budgets provides session- and daily-level token/cost budget
// trackers that pair with vage/largemodel.BudgetMiddleware to enforce hard
// consumption limits across all LLM calls in a vv process.
package budgets

import (
	"sync"
	"time"
)

// DefaultWarnPercent is used when BudgetConfig.WarnPercent is zero.
const DefaultWarnPercent = 0.8

// Scope constants — kept as plain strings to match event payload conventions
// (schema.BudgetWarnData.Scope / BudgetExceededData.Scope).
const (
	ScopeSession = "session"
	ScopeDaily   = "daily"
)

// Config is the minimal shape the tracker constructors consume. It intentionally
// mirrors the Session-* / Daily-* fields of configs.BudgetConfig so callers
// can pass either the session or daily subset by hand.
type Config struct {
	HardTokens  int64
	HardCostUSD float64
	WarnPercent float64
}

// Snapshot is a read-only view for rendering (CLI /budget, HTTP GET /v1/budget).
type Snapshot struct {
	Scope            string    `json:"scope"`
	UsedTokens       int64     `json:"used_tokens"`
	UsedCostUSD      float64   `json:"used_cost_usd,omitempty"`
	HardTokens       int64     `json:"hard_tokens,omitempty"`
	HardCostUSD      float64   `json:"hard_cost_usd,omitempty"`
	WarnPercent      float64   `json:"warn_percent"`
	WindowStart      time.Time `json:"window_start"`
	RemainingTokens  int64     `json:"remaining_tokens"`             // -1 if unlimited
	RemainingCostUSD float64   `json:"remaining_cost_usd,omitempty"` // negative if unlimited
}

// AddResult reports the outcome of accounting a single LLM call.
type AddResult struct {
	WarnCrossed bool                 // true iff this Add first crossed the warn threshold
	Exceeded    *BudgetExceededError // non-nil iff a hard limit is now reached
	Dimension   string               // "tokens" or "cost" — which dimension first triggered WarnCrossed
}

// Tracker aggregates token + cost usage against hard limits. Callers
// pre-compute cost (using costtraces.Pricing) and pass it into Add.
// A nil Tracker is a valid "disabled" sentinel — Check/Add/Snapshot all
// no-op on nil receivers.
type Tracker struct {
	scope       string
	daily       bool
	clock       func() time.Time
	mu          sync.Mutex
	usedTokens  int64
	usedCostUSD float64
	hardTokens  int64
	hardCostUSD float64
	warnPct     float64
	warnFired   bool
	windowStart time.Time
}

// NewSession builds a session-scope Tracker. Returns nil if no hard limits
// are configured (caller treats that as "feature disabled at this layer").
func NewSession(cfg Config) *Tracker {
	return newTracker(ScopeSession, false, cfg, time.Now)
}

// NewDaily builds a daily-scope Tracker with the system clock.
func NewDaily(cfg Config) *Tracker {
	return newTracker(ScopeDaily, true, cfg, time.Now)
}

// NewDailyWithClock is NewDaily with an injected clock for deterministic tests.
func NewDailyWithClock(cfg Config, clock func() time.Time) *Tracker {
	return newTracker(ScopeDaily, true, cfg, clock)
}

func newTracker(scope string, daily bool, cfg Config, clock func() time.Time) *Tracker {
	if cfg.HardTokens <= 0 && cfg.HardCostUSD <= 0 {
		return nil
	}

	warn := cfg.WarnPercent
	if warn <= 0 {
		warn = DefaultWarnPercent
	}

	if clock == nil {
		clock = time.Now
	}

	return &Tracker{
		scope:       scope,
		daily:       daily,
		clock:       clock,
		hardTokens:  cfg.HardTokens,
		hardCostUSD: cfg.HardCostUSD,
		warnPct:     warn,
		windowStart: windowStartFor(daily, clock()),
	}
}

// windowStartFor returns the canonical start time for the current window.
// Session trackers use the tracker's creation time; daily trackers use the
// UTC midnight immediately preceding now.
func windowStartFor(daily bool, now time.Time) time.Time {
	if !daily {
		return now
	}

	u := now.UTC()

	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// Scope returns the tracker's scope label.
func (t *Tracker) Scope() string {
	if t == nil {
		return ""
	}

	return t.scope
}

// Check is the pre-call gate. It returns a non-nil error if the tracker is
// already at or beyond any configured hard limit. Daily trackers roll the
// window forward under the same lock as accounting, so Check() immediately
// after UTC midnight sees the fresh window.
func (t *Tracker) Check() *BudgetExceededError {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.rollLocked()

	return t.exceededLocked()
}

// Add records a single LLM call's usage. tokens is input+output (cache-read
// is already inside input for Anthropic; the caller must not double-add).
// costUSD is the pre-computed dollar figure (0 if pricing is unavailable).
// The returned AddResult tells the caller whether to emit EventBudgetWarn
// and/or EventBudgetExceeded.
func (t *Tracker) Add(tokens int64, costUSD float64) AddResult {
	if t == nil {
		return AddResult{}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.rollLocked()

	// Record pre-Add state for warn-threshold detection.
	prevPctTokens := t.pctTokensLocked()
	prevPctCost := t.pctCostLocked()

	t.usedTokens += tokens
	t.usedCostUSD += costUSD

	result := AddResult{}

	// Detect warn-threshold crossing (first time only).
	if !t.warnFired {
		curPctTokens := t.pctTokensLocked()
		curPctCost := t.pctCostLocked()

		switch {
		case t.hardTokens > 0 && prevPctTokens < t.warnPct && curPctTokens >= t.warnPct:
			t.warnFired = true
			result.WarnCrossed = true
			result.Dimension = "tokens"
		case t.hardCostUSD > 0 && prevPctCost < t.warnPct && curPctCost >= t.warnPct:
			t.warnFired = true
			result.WarnCrossed = true
			result.Dimension = "cost"
		}
	}

	// Detect hard-limit crossing.
	if exceeded := t.exceededLocked(); exceeded != nil {
		result.Exceeded = exceeded
	}

	return result
}

// Snapshot returns a read-only view of the tracker. Safe to call from any
// goroutine. Returns the zero value when called on nil.
func (t *Tracker) Snapshot() Snapshot {
	if t == nil {
		return Snapshot{}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.rollLocked()

	return Snapshot{
		Scope:            t.scope,
		UsedTokens:       t.usedTokens,
		UsedCostUSD:      t.usedCostUSD,
		HardTokens:       t.hardTokens,
		HardCostUSD:      t.hardCostUSD,
		WarnPercent:      t.warnPct,
		WindowStart:      t.windowStart,
		RemainingTokens:  t.remainingTokensLocked(),
		RemainingCostUSD: t.remainingCostLocked(),
	}
}

// rollLocked advances the window if the daily tracker has crossed UTC midnight.
// Caller must hold t.mu.
func (t *Tracker) rollLocked() {
	if !t.daily {
		return
	}

	now := t.clock()
	newStart := windowStartFor(true, now)

	if newStart.After(t.windowStart) {
		t.usedTokens = 0
		t.usedCostUSD = 0
		t.warnFired = false
		t.windowStart = newStart
	}
}

func (t *Tracker) pctTokensLocked() float64 {
	if t.hardTokens <= 0 {
		return 0
	}

	return float64(t.usedTokens) / float64(t.hardTokens)
}

func (t *Tracker) pctCostLocked() float64 {
	if t.hardCostUSD <= 0 {
		return 0
	}

	return t.usedCostUSD / t.hardCostUSD
}

func (t *Tracker) remainingTokensLocked() int64 {
	if t.hardTokens <= 0 {
		return -1
	}

	r := t.hardTokens - t.usedTokens
	if r < 0 {
		return 0
	}

	return r
}

func (t *Tracker) remainingCostLocked() float64 {
	if t.hardCostUSD <= 0 {
		return -1
	}

	r := t.hardCostUSD - t.usedCostUSD
	if r < 0 {
		return 0
	}

	return r
}

// exceededLocked returns the first hard-limit violation, preferring tokens
// over cost when both dimensions are over. Caller must hold t.mu.
func (t *Tracker) exceededLocked() *BudgetExceededError {
	if t.hardTokens > 0 && t.usedTokens >= t.hardTokens {
		return &BudgetExceededError{
			Scope:     t.scope,
			Dimension: "tokens",
			Used:      t.usedTokens,
			Limit:     t.hardTokens,
		}
	}

	if t.hardCostUSD > 0 && t.usedCostUSD >= t.hardCostUSD {
		return &BudgetExceededError{
			Scope:        t.scope,
			Dimension:    "cost",
			UsedCostUSD:  t.usedCostUSD,
			LimitCostUSD: t.hardCostUSD,
		}
	}

	return nil
}
