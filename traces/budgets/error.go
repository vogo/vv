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
	"errors"
	"fmt"
)

// ErrBudgetExceeded is the sentinel wrapped by every BudgetExceededError.
// Callers should test rejection via errors.Is(err, ErrBudgetExceeded).
var ErrBudgetExceeded = errors.New("budget exceeded")

// BudgetExceededError reports that a hard budget limit was reached.
// It wraps ErrBudgetExceeded so errors.Is matches against the sentinel.
type BudgetExceededError struct {
	Scope        string  // "session" | "daily"
	Dimension    string  // "tokens" | "cost"
	Used         int64   // accumulated token count
	Limit        int64   // configured token limit
	UsedCostUSD  float64 // accumulated cost (populated when Dimension == "cost")
	LimitCostUSD float64 // configured cost limit
}

// Error implements error.
func (e *BudgetExceededError) Error() string {
	if e.Dimension == "cost" {
		return fmt.Sprintf("budget exceeded: %s cost %.4f/%.4f USD", e.Scope, e.UsedCostUSD, e.LimitCostUSD)
	}

	return fmt.Sprintf("budget exceeded: %s tokens %d/%d", e.Scope, e.Used, e.Limit)
}

// Unwrap allows errors.Is(err, ErrBudgetExceeded) to match.
func (e *BudgetExceededError) Unwrap() error { return ErrBudgetExceeded }

// IsExceeded reports whether err (or any wrapped error) is a budget rejection.
func IsExceeded(err error) bool { return errors.Is(err, ErrBudgetExceeded) }
