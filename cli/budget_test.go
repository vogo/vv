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

package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/vogo/vv/traces/budgets"
)

func TestRenderBudgetReportNoneConfigured(t *testing.T) {
	out := renderBudgetReport(0, nil, nil, time.Now)

	for _, expect := range []string{"Run:", "Session:", "Daily:", "not configured"} {
		if !strings.Contains(out, expect) {
			t.Fatalf("output missing %q; full output:\n%s", expect, out)
		}
	}
	// "not configured" should appear three times — one per layer.
	if c := strings.Count(out, "not configured"); c != 3 {
		t.Fatalf("expected 3 'not configured' lines, got %d", c)
	}
}

func TestRenderBudgetReportActiveUnderLimit(t *testing.T) {
	session := budgets.NewSession(budgets.Config{HardTokens: 200000, HardCostUSD: 5.0})
	session.Add(15320, 0.12)

	// Inject a clock so the "resets in" string is stable. Daily window starts
	// at UTC midnight, so with now=08:00 we expect ~16h remaining.
	now := func() time.Time { return time.Date(2026, time.April, 23, 8, 0, 0, 0, time.UTC) }
	clk := func() time.Time { return now().Add(-8 * time.Hour) } // window creation at UTC 00:00
	daily := budgets.NewDailyWithClock(budgets.Config{HardTokens: 2_000_000}, clk)
	daily.Add(1_203_000, 0)

	out := renderBudgetReport(50000, session, daily, now)

	for _, want := range []string{
		"budget per run = 50,000 tokens",
		"15,320 / 200,000 tokens",
		"$0.12 / $5.00",
		"1,203,000 / 2,000,000 tokens",
		"resets in",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected substring %q in:\n%s", want, out)
		}
	}
}

func TestRenderBudgetReportExhaustedShowsRemainingZero(t *testing.T) {
	session := budgets.NewSession(budgets.Config{HardTokens: 100})
	session.Add(150, 0) // over-shoot

	out := renderBudgetReport(0, session, nil, time.Now)

	// Session line should show 150/100 tokens and 100% crossing.
	if !strings.Contains(out, "150 / 100 tokens") {
		t.Fatalf("expected 150/100 in output:\n%s", out)
	}
}

func TestFormatIntWithCommas(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{1_234_567, "1,234,567"},
		{-12345, "-12,345"},
	}
	for _, tc := range tests {
		if got := formatIntWithCommas(tc.in); got != tc.want {
			t.Fatalf("formatIntWithCommas(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatWindowRemaining(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{time.Second, "<1m"},
		{2 * time.Minute, "2m"},
		{8 * time.Hour, "~8h"},
	}
	for _, tc := range tests {
		if got := formatWindowRemaining(tc.d); got != tc.want {
			t.Fatalf("formatWindowRemaining(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
