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

package httpapis

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vogo/vv/traces/budgets"
)

func TestHandleGetBudgetBothLayers(t *testing.T) {
	session := budgets.NewSession(budgets.Config{HardTokens: 1000, HardCostUSD: 2.0})
	session.Add(400, 0.5)
	daily := budgets.NewDaily(budgets.Config{HardTokens: 10000})
	daily.Add(250, 0)

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/budget", nil)
	handleGetBudget(session, daily)(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rr.Code)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}

	if _, ok := body["session"]; !ok {
		t.Fatalf("response missing session key: %s", rr.Body.String())
	}
	if _, ok := body["daily"]; !ok {
		t.Fatalf("response missing daily key: %s", rr.Body.String())
	}
}

func TestHandleGetBudgetOmitsNilLayers(t *testing.T) {
	daily := budgets.NewDaily(budgets.Config{HardTokens: 500})
	daily.Add(100, 0)

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/budget", nil)
	handleGetBudget(nil, daily)(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rr.Code)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}

	if _, ok := body["session"]; ok {
		t.Fatalf("session should be omitted when tracker is nil: %s", rr.Body.String())
	}
	if _, ok := body["daily"]; !ok {
		t.Fatalf("daily should be present: %s", rr.Body.String())
	}
}

func TestBudgetErrorMiddlewareRewritesTo429(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"error":"budget exceeded: session tokens 100/100"}`))
	})

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/agents/foo/run", nil)
	budgetErrorMiddleware(upstream).ServeHTTP(rr, r)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status: want 429, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "budget exceeded") {
		t.Fatalf("body should preserve upstream payload; got %s", rr.Body.String())
	}
}

func TestBudgetErrorMiddlewarePassesThroughNonBudget(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	})

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	budgetErrorMiddleware(upstream).ServeHTTP(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ok") {
		t.Fatalf("body should pass through; got %s", rr.Body.String())
	}
}

func TestBudgetErrorMiddlewareEnvelopeError(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"type":"budget_exceeded","message":"daily tokens 2000000/2000000"}}`))
	})

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/agents/foo/run", nil)
	budgetErrorMiddleware(upstream).ServeHTTP(rr, r)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status: want 429, got %d", rr.Code)
	}
}
