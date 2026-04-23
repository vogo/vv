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
	"context"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/largemodel"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/traces/costtraces"
)

// Dispatcher is the minimal event-emitter the wiring helpers need.
// largemodel.DispatchFunc already matches this shape; a nil dispatcher
// is allowed and silently drops events.
type Dispatcher func(ctx context.Context, event schema.Event)

// Wire builds a pair of closures suitable for largemodel.NewBudgetMiddleware.
// The pre-check fires session.Check then daily.Check, returning the first
// non-nil error. The post-record converts aimodel.Usage → (tokens, cost USD)
// using the supplied pricing (may be nil) and applies the result to each
// tracker, emitting EventBudgetWarn / EventBudgetExceeded as thresholds are
// first crossed.
//
// Either tracker may be nil; when both are nil this returns (nil, nil) so the
// caller can skip inserting the middleware entirely.
func Wire(session, daily *Tracker, pricing *costtraces.Pricing, dispatch Dispatcher) (largemodel.BudgetPreCheckFunc, largemodel.BudgetPostRecordFunc) {
	if session == nil && daily == nil {
		return nil, nil
	}

	preCheck := func(_ context.Context) error {
		if err := session.Check(); err != nil {
			return err
		}

		if err := daily.Check(); err != nil {
			return err
		}

		return nil
	}

	postRecord := func(ctx context.Context, u aimodel.Usage) {
		tokens := int64(u.PromptTokens + u.CompletionTokens)

		var costUSD float64
		if pricing != nil {
			nonCached := max(u.PromptTokens-u.CacheReadTokens, 0)
			costUSD = float64(nonCached)/1_000_000*pricing.InputPerMTokens +
				float64(u.CompletionTokens)/1_000_000*pricing.OutputPerMTokens +
				float64(u.CacheReadTokens)/1_000_000*pricing.CachePerMTokens
		}

		apply(ctx, session, tokens, costUSD, dispatch)
		apply(ctx, daily, tokens, costUSD, dispatch)
	}

	return preCheck, postRecord
}

// apply records usage on a single tracker and dispatches any budget events.
// A nil tracker is a no-op so callers can pass session/daily directly.
func apply(ctx context.Context, t *Tracker, tokens int64, costUSD float64, dispatch Dispatcher) {
	if t == nil {
		return
	}

	res := t.Add(tokens, costUSD)

	if dispatch == nil {
		return
	}

	if res.WarnCrossed {
		dispatch(ctx, newWarnEvent(t, res.Dimension))
	}

	if res.Exceeded != nil {
		dispatch(ctx, newExceededEvent(res.Exceeded))
	}
}

func newWarnEvent(t *Tracker, dimension string) schema.Event {
	snap := t.Snapshot()

	pct := 0.0
	switch dimension {
	case "tokens":
		if snap.HardTokens > 0 {
			pct = float64(snap.UsedTokens) / float64(snap.HardTokens)
		}
	case "cost":
		if snap.HardCostUSD > 0 {
			pct = snap.UsedCostUSD / snap.HardCostUSD
		}
	}

	return schema.NewEvent(schema.EventBudgetWarn, "", "", schema.BudgetWarnData{
		Scope:     snap.Scope,
		Dimension: dimension,
		Used:      snap.UsedTokens,
		UsedCost:  snap.UsedCostUSD,
		Limit:     snap.HardTokens,
		LimitCost: snap.HardCostUSD,
		Percent:   pct,
	})
}

func newExceededEvent(e *BudgetExceededError) schema.Event {
	return schema.NewEvent(schema.EventBudgetExceeded, "", "", schema.BudgetExceededData{
		Scope:     e.Scope,
		Dimension: e.Dimension,
		Used:      e.Used,
		UsedCost:  e.UsedCostUSD,
		Limit:     e.Limit,
		LimitCost: e.LimitCostUSD,
	})
}
