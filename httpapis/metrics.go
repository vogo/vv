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
	"errors"
	"net/http"
	"strings"

	vctx "github.com/vogo/vage/context"
	"github.com/vogo/vage/session"
	"github.com/vogo/vv/setup"
)

// metricsStore safely extracts the MetricsStore from initResult. nil
// initResult or nil field both yield nil so handleGetMetrics returns
// 503 — keeping the wiring resilient to call sites that build the
// HTTP server without going through setup.Init (eval helpers, etc.).
func metricsStore(initResult *setup.InitResult) session.MetricsStore {
	if initResult == nil {
		return nil
	}
	return initResult.MetricsStore
}

// buildReportReader extracts the read side of the BuildReport
// archive. The wider Sink interface is what setup wires; here we
// expose just the reader contract because the HTTP layer only needs
// to list, never to write.
func buildReportReader(initResult *setup.InitResult) vctx.BuildReportReader {
	if initResult == nil || initResult.BuildReportSink == nil {
		return nil
	}
	if r, ok := initResult.BuildReportSink.(vctx.BuildReportReader); ok {
		return r
	}
	return nil
}

// httpBuildReportsDefaultLimit is the default page size for
// /build-reports when the caller omits ?limit. Aligned with the
// session-list defaults so dashboards do not get surprised by a
// different bound.
const (
	httpBuildReportsDefaultLimit = 20
	httpBuildReportsMaxLimit     = 200 // hard cap; vctx imposes its own MaxListLimit too
)

// buildReportsResponse wraps the report list so a future addition
// (pagination metadata, total count) does not break the wire format.
type buildReportsResponse struct {
	Reports []vctx.BuildReport `json:"reports"`
}

// handleGetMetrics implements GET /v1/sessions/{id}/metrics.
//
// Status mapping:
//   - 200: SessionMetrics JSON
//   - 400: empty session id
//   - 404: ErrMetricsNotFound (no Update has yet recorded anything)
//   - 503: store is nil — happens only if vv is configured with
//     session.enabled=true but a wiring regression dropped the metrics
//     subsystem. Better to surface than to mask as 200+zeros.
//   - 500: any other store error
func handleGetMetrics(store session.MetricsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"code":    "metrics_disabled",
				"message": "metrics subsystem is not configured",
			})
			return
		}

		sid := strings.TrimSpace(r.PathValue("id"))
		if sid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "bad_request", "message": "session id is empty",
			})
			return
		}

		got, err := store.Get(r.Context(), sid)
		switch {
		case errors.Is(err, session.ErrMetricsNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{
				"code":    "metrics_not_found",
				"message": "no metrics recorded for this session yet",
			})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"code": "metrics_load_failed", "message": err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, got)
	}
}

// handleListBuildReports implements
// GET /v1/sessions/{id}/build-reports?limit=N.
//
// Status mapping:
//   - 200: { reports: [...] }, newest-first, capped at limit
//   - 400: empty session id or malformed limit
//   - 503: reader is nil (not configured / disabled by config)
//   - 500: scan / decode error
//
// A session with no reports yet returns 200 + empty list rather than
// 404 — clients should treat "no observations" as a valid state.
func handleListBuildReports(reader vctx.BuildReportReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if reader == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"code":    "build_reports_disabled",
				"message": "build_report archive is not configured (set session.persist_build_reports: true)",
			})
			return
		}

		sid := strings.TrimSpace(r.PathValue("id"))
		if sid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "bad_request", "message": "session id is empty",
			})
			return
		}

		limit, ok := parsePositiveInt(r.URL.Query().Get("limit"),
			httpBuildReportsDefaultLimit, httpBuildReportsMaxLimit)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "bad_request", "message": "invalid limit",
			})
			return
		}

		reports, err := reader.List(r.Context(), sid, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"code": "build_reports_load_failed", "message": err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, buildReportsResponse{Reports: reports})
	}
}
