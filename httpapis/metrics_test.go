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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	vctx "github.com/vogo/vage/context"
	"github.com/vogo/vage/session"
)

// newMetricsRequest builds a httptest GET against the metrics route
// shape so r.PathValue("id") works the same way as the live mux.
func newMetricsRequest(sid string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sid+"/metrics", nil)
	req.SetPathValue("id", sid)
	return req
}

func newBuildReportsRequest(sid, query string) *http.Request {
	target := "/v1/sessions/" + sid + "/build-reports"
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetPathValue("id", sid)
	return req
}

// TestHandleGetMetrics_NilStore_503 surfaces the wiring regression as
// a structured error so the user can debug rather than get a 200 of
// zeros that looks legit.
func TestHandleGetMetrics_NilStore_503(t *testing.T) {
	h := handleGetMetrics(nil)

	rec := httptest.NewRecorder()
	h(rec, newMetricsRequest("sid"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestHandleGetMetrics_EmptyID_400 guards the obvious caller error.
func TestHandleGetMetrics_EmptyID_400(t *testing.T) {
	h := handleGetMetrics(session.NewMapMetricsStore())

	rec := httptest.NewRecorder()
	h(rec, newMetricsRequest(""))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestHandleGetMetrics_NotFound_404 returns 404 for a session that
// has never been Updated. Clients should treat this as "no metrics
// yet" rather than fault.
func TestHandleGetMetrics_NotFound_404(t *testing.T) {
	h := handleGetMetrics(session.NewMapMetricsStore())

	rec := httptest.NewRecorder()
	h(rec, newMetricsRequest("ghost-sid"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "metrics_not_found" {
		t.Errorf("code = %q", body["code"])
	}
}

// TestHandleGetMetrics_HappyPath records a few updates and reads them
// back through the HTTP envelope. Verifies the wire format matches
// the Go struct so dashboards do not break on a missed JSON tag.
func TestHandleGetMetrics_HappyPath(t *testing.T) {
	store := session.NewMapMetricsStore()

	if err := store.Update(context.Background(), "sid-ok", func(m *session.SessionMetrics) {
		m.PromptTokens = 100
		m.CompletionTokens = 25
		m.CostUSD = 0.42
		m.ResumeCount = 2
		m.ContextEdits = 4
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	h := handleGetMetrics(store)
	rec := httptest.NewRecorder()
	h(rec, newMetricsRequest("sid-ok"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got session.SessionMetrics
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.SessionID != "sid-ok" {
		t.Errorf("SessionID = %q", got.SessionID)
	}
	if got.PromptTokens != 100 || got.CompletionTokens != 25 {
		t.Errorf("token counters = %+v", got)
	}
	if got.TotalTokens != 125 {
		t.Errorf("TotalTokens = %d, want 125 (auto-derived)", got.TotalTokens)
	}
	if got.CostUSD < 0.41 || got.CostUSD > 0.43 {
		t.Errorf("CostUSD = %f, want ≈0.42", got.CostUSD)
	}
	if got.ResumeCount != 2 {
		t.Errorf("ResumeCount = %d", got.ResumeCount)
	}
}

// TestHandleListBuildReports_NilReader_503 mirrors the metrics path:
// missing wiring should be obvious, not silent.
func TestHandleListBuildReports_NilReader_503(t *testing.T) {
	h := handleListBuildReports(nil)

	rec := httptest.NewRecorder()
	h(rec, newBuildReportsRequest("sid", ""))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestHandleListBuildReports_EmptyID_400 is the obvious-error guard.
func TestHandleListBuildReports_EmptyID_400(t *testing.T) {
	sink, err := vctx.NewFileBuildReportSink(t.TempDir())
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	h := handleListBuildReports(sink)

	rec := httptest.NewRecorder()
	h(rec, newBuildReportsRequest("", ""))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestHandleListBuildReports_BadLimit_400 covers a non-numeric ?limit
// query parameter.
func TestHandleListBuildReports_BadLimit_400(t *testing.T) {
	sink, err := vctx.NewFileBuildReportSink(t.TempDir())
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	h := handleListBuildReports(sink)

	rec := httptest.NewRecorder()
	h(rec, newBuildReportsRequest("sid", "limit=banana"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestHandleListBuildReports_EmptySession_200 returns an empty list
// for a session with no archived reports yet — clients should treat
// "no observations" as a valid state, not 404.
func TestHandleListBuildReports_EmptySession_200(t *testing.T) {
	sink, err := vctx.NewFileBuildReportSink(t.TempDir())
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	h := handleListBuildReports(sink)

	rec := httptest.NewRecorder()
	h(rec, newBuildReportsRequest("ghost-sid", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body buildReportsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Reports) != 0 {
		t.Errorf("reports = %d, want 0", len(body.Reports))
	}
}

// TestHandleListBuildReports_HappyPath writes 5 reports and reads
// them back capped at 3, newest-first.
func TestHandleListBuildReports_HappyPath(t *testing.T) {
	sink, err := vctx.NewFileBuildReportSink(t.TempDir())
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	for i := range 5 {
		if err := sink.Save(context.Background(), "sid-bp", vctx.BuildReport{
			OutputCount: i + 1,
		}); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	h := handleListBuildReports(sink)
	rec := httptest.NewRecorder()
	h(rec, newBuildReportsRequest("sid-bp", "limit=3"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body buildReportsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Reports) != 3 {
		t.Fatalf("reports = %d, want 3", len(body.Reports))
	}
	// Newest-first: OutputCount 5,4,3.
	for i, want := range []int{5, 4, 3} {
		if body.Reports[i].OutputCount != want {
			t.Errorf("reports[%d].OutputCount = %d, want %d",
				i, body.Reports[i].OutputCount, want)
		}
	}
}

// TestHandleListBuildReports_LimitClamped covers the upper bound: a
// caller that asks for far more than the route's local cap (200)
// must not get more than the cap, and certainly not the underlying
// store's MaxListLimit.
func TestHandleListBuildReports_LimitClamped(t *testing.T) {
	sink, err := vctx.NewFileBuildReportSink(t.TempDir(),
		vctx.WithBuildReportLimit(httpBuildReportsMaxLimit*2))
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	for i := range 50 {
		_ = sink.Save(context.Background(), "sid-clamp", vctx.BuildReport{OutputCount: i})
	}

	h := handleListBuildReports(sink)

	rec := httptest.NewRecorder()
	h(rec, newBuildReportsRequest("sid-clamp", "limit=99999"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body buildReportsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Reports) > httpBuildReportsMaxLimit {
		t.Errorf("reports = %d, want <= %d", len(body.Reports), httpBuildReportsMaxLimit)
	}
}
