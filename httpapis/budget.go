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
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/vogo/vv/traces/budgets"
)

// handleGetBudget returns a JSON view of the session and daily trackers.
// Nil trackers are omitted so clients can detect which layers are active.
func handleGetBudget(session, daily *budgets.Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		resp := make(map[string]any, 2)

		if session != nil {
			resp["session"] = session.Snapshot()
		}

		if daily != nil {
			resp["daily"] = daily.Snapshot()
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// budgetErrorMiddleware rewrites service responses whose JSON body contains
// a structural budget-exceeded error to HTTP 429 with a stable envelope. It
// is a narrow complement to costEnrichMiddleware: we cannot change the
// upstream service layer's status code otherwise, but vv/traces/budgets
// errors propagate through the agent run path in the response body.
func budgetErrorMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &responseRecorder{
			ResponseWriter: w,
			body:           &bytes.Buffer{},
			statusCode:     http.StatusOK,
		}

		next.ServeHTTP(rec, r)

		body := rec.body.Bytes()
		status := rec.statusCode

		if isJSONContent(rec.Header()) && containsBudgetExceeded(body) {
			status = http.StatusTooManyRequests
		}

		for k, vs := range rec.Header() {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}

		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}

// containsBudgetExceeded reports whether the JSON body carries the signature
// phrase emitted by *budgets.BudgetExceededError.Error(). Using the error
// text avoids tight coupling to a specific JSON schema while still catching
// both the top-level error field and any nested envelope.
func containsBudgetExceeded(body []byte) bool {
	if len(body) == 0 {
		return false
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}

	if raw, ok := probe["error"]; ok {
		var msg string
		if err := json.Unmarshal(raw, &msg); err == nil && looksLikeBudgetExceeded(msg) {
			return true
		}

		var obj struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		}
		if err := json.Unmarshal(raw, &obj); err == nil {
			if obj.Type == "budget_exceeded" || looksLikeBudgetExceeded(obj.Message) {
				return true
			}
		}
	}

	return false
}

func looksLikeBudgetExceeded(s string) bool {
	return len(s) > 0 && bytes.Contains([]byte(s), []byte("budget exceeded"))
}
