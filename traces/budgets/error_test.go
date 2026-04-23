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
	"strings"
	"testing"
)

func TestErrBudgetExceededIsSentinel(t *testing.T) {
	err := &BudgetExceededError{Scope: ScopeSession, Dimension: "tokens", Used: 10, Limit: 10}
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatal("errors.Is should match ErrBudgetExceeded")
	}
	if !IsExceeded(err) {
		t.Fatal("IsExceeded should return true for BudgetExceededError")
	}
}

func TestIsExceededOnUnrelatedErrors(t *testing.T) {
	if IsExceeded(nil) {
		t.Fatal("IsExceeded(nil) should be false")
	}
	if IsExceeded(fmt.Errorf("other")) {
		t.Fatal("IsExceeded on unrelated error should be false")
	}
}

func TestErrorStringFormat(t *testing.T) {
	tests := []struct {
		name string
		err  *BudgetExceededError
		want string
	}{
		{
			"tokens dimension",
			&BudgetExceededError{Scope: ScopeSession, Dimension: "tokens", Used: 100, Limit: 100},
			"budget exceeded: session tokens 100/100",
		},
		{
			"cost dimension",
			&BudgetExceededError{Scope: ScopeDaily, Dimension: "cost", UsedCostUSD: 1.25, LimitCostUSD: 1.0},
			"budget exceeded: daily cost",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); !strings.Contains(got, tc.want) {
				t.Fatalf("Error(): want substring %q, got %q", tc.want, got)
			}
		})
	}
}

func TestWrappedSentinelMatchesErrorsIs(t *testing.T) {
	wrapped := fmt.Errorf("mw: %w", &BudgetExceededError{Scope: ScopeDaily, Dimension: "tokens", Used: 1, Limit: 1})
	if !errors.Is(wrapped, ErrBudgetExceeded) {
		t.Fatal("wrapped sentinel should be detectable via errors.Is")
	}
}
