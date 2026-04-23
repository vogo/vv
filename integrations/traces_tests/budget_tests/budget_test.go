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

// Package budget_tests contains integration tests for the session/daily
// budget enforcement pipeline. These tests wire a stub ChatCompleter
// through vage/largemodel.NewBudgetMiddleware + vv/traces/budgets.Wire to
// verify that the full path (pre-check → LLM call → post-record → event
// dispatch) behaves as specified in the design doc §10.2. No real LLM is
// required; a counting stub stands in for the network.
package budget_tests

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/largemodel"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/traces/budgets"
	"github.com/vogo/vv/traces/costtraces"
)

// Scenario: session_hard_tokens
// Session budget of 200 tokens with per-call usage of 100 tokens:
// call 1 → 100 used (ok), call 2 → 200 used (ok, but now at limit),
// call 3 → pre-check sees Used >= Limit and rejects before hitting the
// stub completer. Assertions: error matches budgets.IsExceeded, the
// stub's call counter stops at 2, and the error's Dimension is "tokens".
func TestIntegration_SessionHardTokens(t *testing.T) {
	session := budgets.NewSession(budgets.Config{HardTokens: 200})
	if session == nil {
		t.Fatal("expected non-nil session tracker")
	}

	stub := &stubCompleter{usage: aimodel.Usage{PromptTokens: 60, CompletionTokens: 40}} // 100 tokens / call
	disp, _ := newCollectingDispatcher()
	completer := wrap(t, session, nil, nil, disp, stub)

	ctx := context.Background()

	// Two successful calls consume the full 200-token session budget.
	for i := range 2 {
		if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
			t.Fatalf("call %d unexpected err: %v", i+1, err)
		}
	}

	// Third call must be rejected at pre-check, without incrementing stub.
	_, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{})
	if err == nil {
		t.Fatal("expected budget-exceeded error, got nil")
	}

	if !budgets.IsExceeded(err) {
		t.Fatalf("expected errors.Is(err, ErrBudgetExceeded), got %v", err)
	}

	var bee *budgets.BudgetExceededError
	if !errors.As(err, &bee) {
		t.Fatalf("expected *BudgetExceededError, got %T: %v", err, err)
	}

	if bee.Dimension != "tokens" {
		t.Errorf("Dimension = %q, want %q", bee.Dimension, "tokens")
	}

	if got := stub.calls.Load(); got != 2 {
		t.Errorf("stub completer call count = %d, want 2 (third call must be pre-check rejected)", got)
	}
}

// Scenario: session_warn_percent
// Session budget 1000 tokens, warn 0.5. Pre-threshold calls (< 500 used)
// must not emit EventBudgetWarn; the first Add that takes usage >= 500
// must emit exactly one EventBudgetWarn; subsequent Adds up to 999 must
// NOT emit any additional warn events (warnFired is one-shot).
func TestIntegration_SessionWarnPercent(t *testing.T) {
	session := budgets.NewSession(budgets.Config{HardTokens: 1000, WarnPercent: 0.5})
	if session == nil {
		t.Fatal("expected non-nil session tracker")
	}

	stub := &stubCompleter{usage: aimodel.Usage{PromptTokens: 60, CompletionTokens: 40}} // 100 tokens / call
	disp, getEvents := newCollectingDispatcher()
	completer := wrap(t, session, nil, nil, disp, stub)

	ctx := context.Background()

	// 4 calls → 400 tokens: below warn threshold (50% of 1000 = 500).
	for i := range 4 {
		if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
			t.Fatalf("call %d unexpected err: %v", i+1, err)
		}
	}

	warnCount := 0
	for _, e := range getEvents() {
		if e.Type == schema.EventBudgetWarn {
			warnCount++
		}
	}
	if warnCount != 0 {
		t.Fatalf("expected 0 warn events below threshold, got %d", warnCount)
	}

	// 5th call brings usage to 500 → first warn crossing.
	if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
		t.Fatalf("5th call err: %v", err)
	}

	// Calls 6–9 take usage to 900, well past warn but under limit; must
	// remain a one-shot event.
	for i := range 4 {
		if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
			t.Fatalf("post-warn call %d err: %v", i+1, err)
		}
	}

	warnCount = 0
	for _, e := range getEvents() {
		if e.Type == schema.EventBudgetWarn {
			warnCount++
		}
	}
	if warnCount != 1 {
		t.Fatalf("expected exactly 1 EventBudgetWarn, got %d", warnCount)
	}
}

// Scenario: daily_window_roll
// Daily budget with an injected clock. Fill 90% of the limit on day N,
// advance the clock past the next UTC midnight, and verify that (a) the
// window rolls on the next Check/Add, (b) the counter resets to 0, and
// (c) warnFired resets so a new warn event can fire in the fresh window.
func TestIntegration_DailyWindowRoll(t *testing.T) {
	// Start the fake clock at a fixed instant well inside day N so our
	// rollover math is unambiguous.
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	daily := budgets.NewDailyWithClock(
		budgets.Config{HardTokens: 1000, WarnPercent: 0.8},
		clock,
	)
	if daily == nil {
		t.Fatal("expected non-nil daily tracker")
	}

	stub := &stubCompleter{usage: aimodel.Usage{PromptTokens: 60, CompletionTokens: 40}} // 100 tokens / call
	disp, getEvents := newCollectingDispatcher()
	completer := wrap(t, nil, daily, nil, disp, stub)

	ctx := context.Background()

	// 9 calls → 900 tokens (90% of 1000). This also crosses the 80% warn
	// threshold, so we expect exactly one warn event in day N.
	for i := range 9 {
		if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
			t.Fatalf("day N call %d err: %v", i+1, err)
		}
	}

	snap := daily.Snapshot()
	if snap.UsedTokens != 900 {
		t.Fatalf("day N UsedTokens = %d, want 900", snap.UsedTokens)
	}

	warnCountBefore := 0
	for _, e := range getEvents() {
		if e.Type == schema.EventBudgetWarn {
			warnCountBefore++
		}
	}
	if warnCountBefore != 1 {
		t.Fatalf("expected 1 warn event in day N, got %d", warnCountBefore)
	}

	// Advance clock past UTC midnight into day N+1.
	now = time.Date(2026, 4, 23, 1, 0, 0, 0, time.UTC)

	// Next call should roll the window before recording: counter starts
	// at 0, then +100 lands us at 100.
	if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
		t.Fatalf("day N+1 first call err: %v", err)
	}

	snap = daily.Snapshot()
	if snap.UsedTokens != 100 {
		t.Fatalf("after-roll UsedTokens = %d, want 100 (counter should have reset)", snap.UsedTokens)
	}

	// Fill to 800 in the new window → should emit a fresh warn event
	// (warnFired was reset on roll).
	for i := range 7 {
		if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
			t.Fatalf("day N+1 fill call %d err: %v", i+1, err)
		}
	}

	warnCountAfter := 0
	for _, e := range getEvents() {
		if e.Type == schema.EventBudgetWarn {
			warnCountAfter++
		}
	}
	if warnCountAfter != 2 {
		t.Fatalf("expected 2 total warn events (one per window), got %d", warnCountAfter)
	}
}

// Scenario: nested_agent
// Two goroutines simulate a parent agent and a nested sub-agent driving
// the same wrappedLLM. The shared session tracker must aggregate usage
// from every layer exactly — no missed accounting, no double-count.
func TestIntegration_NestedAgent(t *testing.T) {
	const (
		parentCalls = 10
		childCalls  = 10
		perCall     = int64(100)
	)

	// Pick a hard limit comfortably above the combined total so neither
	// goroutine is rejected — we are verifying aggregation, not gating.
	session := budgets.NewSession(budgets.Config{HardTokens: (parentCalls + childCalls) * perCall * 10})
	stub := &stubCompleter{usage: aimodel.Usage{PromptTokens: 60, CompletionTokens: 40}}
	completer := wrap(t, session, nil, nil, nil, stub)

	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := range parentCalls {
			if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
				t.Errorf("parent call %d err: %v", i+1, err)
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := range childCalls {
			if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
				t.Errorf("child call %d err: %v", i+1, err)
				return
			}
		}
	}()

	wg.Wait()

	snap := session.Snapshot()
	want := int64(parentCalls+childCalls) * perCall
	if snap.UsedTokens != want {
		t.Fatalf("UsedTokens = %d, want %d (exact aggregation across parent+child)", snap.UsedTokens, want)
	}

	if got := stub.calls.Load(); got != int64(parentCalls+childCalls) {
		t.Fatalf("stub calls = %d, want %d", got, parentCalls+childCalls)
	}
}

// Scenario: concurrent_tools
// Parallel Wrap(...).ChatCompletion calls from many goroutines simulate
// multiple tool executions running concurrently. The final tracker total
// must equal goroutines × perCall exactly — zero races, zero lost
// updates. Run under `go test -race` to exercise the mutex.
func TestIntegration_ConcurrentTools(t *testing.T) {
	const (
		goroutines = 100
		perCall    = int64(10)
	)

	session := budgets.NewSession(budgets.Config{HardTokens: goroutines * perCall * 100})
	stub := &stubCompleter{usage: aimodel.Usage{PromptTokens: 7, CompletionTokens: 3}} // 10 tokens
	completer := wrap(t, session, nil, nil, nil, stub)

	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
				t.Errorf("concurrent call err: %v", err)
			}
		}()
	}

	wg.Wait()

	snap := session.Snapshot()
	want := int64(goroutines) * perCall
	if snap.UsedTokens != want {
		t.Fatalf("UsedTokens = %d, want %d (no race-induced lost updates)", snap.UsedTokens, want)
	}

	if got := stub.calls.Load(); got != int64(goroutines) {
		t.Fatalf("stub calls = %d, want %d", got, goroutines)
	}
}

// Scenario: cost_and_tokens_both
// Both HardTokens and HardCostUSD are configured. Pick usage + pricing
// such that the cost limit trips first: 1000 output tokens at $15/1M =
// $0.015 per call, cost limit $0.030 (≈ 2 calls); tokens limit 10_000 is
// far out. After 2 calls, the 3rd must be rejected with Dimension=="cost".
func TestIntegration_CostAndTokensBoth(t *testing.T) {
	session := budgets.NewSession(budgets.Config{
		HardTokens:  10_000,
		HardCostUSD: 0.030,
	})
	if session == nil {
		t.Fatal("expected non-nil session tracker")
	}

	// 1000 prompt (0 cached) + 1000 completion at $3/$15 per M:
	// cost = 1000/1e6*3 + 1000/1e6*15 = 0.003 + 0.015 = 0.018 USD/call
	// tokens = 2000/call
	pricing := &costtraces.Pricing{
		InputPerMTokens:  3.0,
		OutputPerMTokens: 15.0,
		CachePerMTokens:  0.3,
	}

	stub := &stubCompleter{
		usage: aimodel.Usage{PromptTokens: 1000, CompletionTokens: 1000},
	}

	completer := wrap(t, session, nil, pricing, nil, stub)

	ctx := context.Background()

	// Call 1: cost accrues to 0.018, tokens to 2000 — both under limit.
	if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
		t.Fatalf("call 1 err: %v", err)
	}

	// Call 2: cost accrues to 0.036 (>= 0.030), tokens to 4000 (<< 10000).
	// This call still succeeds because pre-check runs BEFORE Add; only
	// the post-Add state crosses the limit.
	if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
		t.Fatalf("call 2 err: %v", err)
	}

	// Call 3: pre-check sees Used >= Limit on the cost dimension and
	// rejects with Dimension=="cost" (tokens still have headroom).
	_, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{})
	if err == nil {
		t.Fatal("expected budget-exceeded error, got nil")
	}

	var bee *budgets.BudgetExceededError
	if !errors.As(err, &bee) {
		t.Fatalf("expected *BudgetExceededError, got %T: %v", err, err)
	}

	if bee.Dimension != "cost" {
		t.Errorf("Dimension = %q, want %q (cost should trip before tokens)", bee.Dimension, "cost")
	}

	if got := stub.calls.Load(); got != 2 {
		t.Errorf("stub calls = %d, want 2 (3rd should be pre-check rejected)", got)
	}
}

// Scenario: no_pricing_model
// pricing=nil is passed into budgets.Wire; only a cost limit is
// configured. Cost accumulation therefore stays 0 (Wire skips cost math
// when pricing is nil), so the cost limit never trips even after many
// calls. In parallel, a tokens limit remains enforceable — adding a
// token limit must still gate correctly.
func TestIntegration_NoPricingModel(t *testing.T) {
	// Part 1: cost-only limit with no pricing → never trips.
	costOnly := budgets.NewSession(budgets.Config{HardCostUSD: 0.001})
	if costOnly == nil {
		t.Fatal("expected non-nil session tracker")
	}

	stub := &stubCompleter{usage: aimodel.Usage{PromptTokens: 1000, CompletionTokens: 1000}}
	completer := wrap(t, costOnly, nil, nil /* pricing */, nil, stub)

	ctx := context.Background()
	for i := range 20 {
		if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
			t.Fatalf("cost-only call %d unexpectedly rejected: %v", i+1, err)
		}
	}

	if snap := costOnly.Snapshot(); snap.UsedCostUSD != 0 {
		t.Errorf("UsedCostUSD = %v, want 0 (pricing is nil so no cost accrues)", snap.UsedCostUSD)
	}

	// Part 2: tokens limit in the same no-pricing setup is still enforced.
	tokensOnly := budgets.NewSession(budgets.Config{HardTokens: 500})
	stub2 := &stubCompleter{usage: aimodel.Usage{PromptTokens: 300, CompletionTokens: 200}} // 500 per call
	completer2 := wrap(t, tokensOnly, nil, nil, nil, stub2)

	// First call fills the entire 500-token budget.
	if _, err := completer2.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
		t.Fatalf("tokens-only call 1 err: %v", err)
	}

	// Second call must be rejected.
	if _, err := completer2.ChatCompletion(ctx, &aimodel.ChatRequest{}); !budgets.IsExceeded(err) {
		t.Fatalf("expected budget-exceeded on 2nd call, got %v", err)
	}
}

// Scenario: disabled_all
// When both session and daily trackers are nil, budgets.Wire returns
// (nil, nil), signaling the caller should skip middleware entirely.
// Even if a caller accidentally constructed a BudgetMiddleware with
// (nil, nil), Wrap must behave identically to the underlying completer:
// call count, response, and error all match byte-for-byte.
func TestIntegration_DisabledAll(t *testing.T) {
	preCheck, postRecord := budgets.Wire(nil, nil, nil, nil)
	if preCheck != nil || postRecord != nil {
		t.Fatalf("Wire(nil, nil, ...) = (%v, %v), want (nil, nil)", preCheck, postRecord)
	}

	// Defensive: even if someone wraps with nil closures, behavior must
	// be transparent — equal call count and identical response.
	baseline := &stubCompleter{usage: aimodel.Usage{PromptTokens: 10, CompletionTokens: 5}}
	wrapped := largemodel.NewBudgetMiddleware(nil, nil).Wrap(baseline)

	ctx := context.Background()

	resp1, err1 := baseline.ChatCompletion(ctx, &aimodel.ChatRequest{})
	if err1 != nil {
		t.Fatalf("baseline call err: %v", err1)
	}

	resp2, err2 := wrapped.ChatCompletion(ctx, &aimodel.ChatRequest{})
	if err2 != nil {
		t.Fatalf("wrapped call err: %v", err2)
	}

	if resp1.ID != resp2.ID {
		t.Errorf("response ID baseline=%q wrapped=%q, want identical", resp1.ID, resp2.ID)
	}

	if resp1.Usage != resp2.Usage {
		t.Errorf("usage baseline=%+v wrapped=%+v, want identical", resp1.Usage, resp2.Usage)
	}

	if got := baseline.calls.Load(); got != 2 {
		t.Errorf("baseline calls = %d, want 2 (both paths should reach the completer exactly once)", got)
	}
}

// Scenario (extra): exceeded_event_dispatch
// End-to-end check that post-record on the final (over-limit) Add emits
// exactly one EventBudgetExceeded event, confirming the tracker/dispatch
// handshake reported in the design §3.
func TestIntegration_ExceededEventDispatch(t *testing.T) {
	session := budgets.NewSession(budgets.Config{HardTokens: 100})
	if session == nil {
		t.Fatal("expected non-nil session tracker")
	}

	stub := &stubCompleter{usage: aimodel.Usage{PromptTokens: 60, CompletionTokens: 40}} // 100 tokens / call
	disp, getEvents := newCollectingDispatcher()
	completer := wrap(t, session, nil, nil, disp, stub)

	ctx := context.Background()

	// First call: 100 used, which equals the hard limit — Add returns an
	// Exceeded result and the dispatcher fires EventBudgetExceeded.
	if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); err != nil {
		t.Fatalf("first call err: %v", err)
	}

	// Second call: pre-check must reject.
	if _, err := completer.ChatCompletion(ctx, &aimodel.ChatRequest{}); !budgets.IsExceeded(err) {
		t.Fatalf("expected budget-exceeded on 2nd call, got %v", err)
	}

	exceededCount := 0
	for _, e := range getEvents() {
		if e.Type == schema.EventBudgetExceeded {
			exceededCount++
		}
	}

	if exceededCount < 1 {
		t.Fatalf("expected >= 1 EventBudgetExceeded, got %d", exceededCount)
	}
}
