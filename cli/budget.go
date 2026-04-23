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
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vogo/vv/traces/budgets"
)

// handleBudgetCommand renders a three-line snapshot of Run, Session, and
// Daily budgets. Layers without configured limits render "not configured".
func (m *model) handleBudgetCommand() tea.Cmd {
	return m.printSystem(renderBudgetReport(
		m.app.cfg.Agents.RunTokenBudget,
		m.app.sessionBudget,
		m.app.dailyBudget,
		time.Now,
	))
}

// renderBudgetReport builds the /budget multi-line output. Broken out for
// testability — the caller injects the clock so daily window-remaining math
// is deterministic in tests.
func renderBudgetReport(runBudget int, session, daily *budgets.Tracker, now func() time.Time) string {
	var sb strings.Builder

	sb.WriteString("Budget status:\n")
	sb.WriteString("  Run:     ")
	sb.WriteString(renderRunBudgetLine(runBudget))
	sb.WriteString("\n  Session: ")
	sb.WriteString(renderTrackerLine(session, nil))
	sb.WriteString("\n  Daily:   ")
	sb.WriteString(renderTrackerLine(daily, now))

	return sb.String()
}

func renderRunBudgetLine(runBudget int) string {
	if runBudget <= 0 {
		return "not configured"
	}

	return fmt.Sprintf("budget per run = %s tokens", formatIntWithCommas(int64(runBudget)))
}

// renderTrackerLine renders a single tracker status row. A nil tracker means
// "layer disabled". For daily trackers, now is used to compute the window
// reset countdown.
func renderTrackerLine(t *budgets.Tracker, now func() time.Time) string {
	if t == nil {
		return "not configured"
	}

	snap := t.Snapshot()

	segments := make([]string, 0, 3)

	if snap.HardTokens > 0 {
		pct := float64(snap.UsedTokens) / float64(snap.HardTokens) * 100

		segments = append(segments, fmt.Sprintf("%s / %s tokens (%.1f%%)",
			formatIntWithCommas(snap.UsedTokens),
			formatIntWithCommas(snap.HardTokens),
			pct))
	}

	if snap.HardCostUSD > 0 {
		segments = append(segments, fmt.Sprintf("$%.2f / $%.2f", snap.UsedCostUSD, snap.HardCostUSD))
	}

	if len(segments) == 0 {
		return "not configured"
	}

	line := strings.Join(segments, "  ")

	// Append window reset hint for daily trackers only. The caller signals
	// "daily" by passing a non-nil now func; session trackers pass nil so
	// we skip the reset countdown (their window is the process lifetime).
	if now != nil && snap.Scope == budgets.ScopeDaily && !snap.WindowStart.IsZero() {
		nextReset := snap.WindowStart.Add(24 * time.Hour)
		remaining := nextReset.Sub(now())

		if remaining > 0 {
			line += fmt.Sprintf("  (resets in %s)", formatWindowRemaining(remaining))
		}
	}

	return line
}

// formatIntWithCommas formats a non-negative integer with thousands
// separators, e.g. 1_200_000 -> "1,200,000".
func formatIntWithCommas(n int64) string {
	if n < 0 {
		return fmt.Sprintf("-%s", formatIntWithCommas(-n))
	}

	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}

	var b strings.Builder
	first := len(s) % 3
	if first > 0 {
		b.WriteString(s[:first])
	}

	for i := first; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}

	return b.String()
}

// formatWindowRemaining formats a positive duration compactly:
//   - >= 1h: "~Xh"
//   - >= 1m: "Xm"
//   - else:  "<1m"
func formatWindowRemaining(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("~%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return "<1m"
	}
}
