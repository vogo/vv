package httpapis

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/vogo/aimodel"
	"github.com/vogo/vv/costtracker"
)

// costEnrichMiddleware wraps an HTTP handler to enrich responses with cost data.
// For sync run responses, it injects estimated_cost_usd.
// For streaming responses, it accumulates token counts from llm_call_end events
// and emits a final usage SSE event after the stream ends.
func costEnrichMiddleware(next http.Handler, pricingLookup func(string) *costtracker.Pricing) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Only enrich agent run and stream endpoints.
		switch {
		case strings.Contains(path, "/run"):
			enrichSyncResponse(next, w, r, pricingLookup)
		case strings.Contains(path, "/stream"):
			enrichStreamResponse(next, w, r, pricingLookup)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// enrichSyncResponse captures the response body, injects estimated_cost_usd, and re-writes.
func enrichSyncResponse(next http.Handler, w http.ResponseWriter, r *http.Request, pricingLookup func(string) *costtracker.Pricing) {
	rec := &responseRecorder{
		ResponseWriter: w,
		body:           &bytes.Buffer{},
		statusCode:     http.StatusOK,
	}

	next.ServeHTTP(rec, r)

	body := rec.body.Bytes()

	// Only enrich JSON responses with usage data.
	if rec.statusCode == http.StatusOK && isJSONContent(rec.Header()) {
		body = injectCostIntoJSON(body, pricingLookup)
	}

	// Copy headers from the recorder to the actual ResponseWriter.
	for k, vs := range rec.Header() {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(rec.statusCode)
	_, _ = w.Write(body)
}

// injectCostIntoJSON parses the response, calculates cost from usage, and injects estimated_cost_usd.
func injectCostIntoJSON(body []byte, pricingLookup func(string) *costtracker.Pricing) []byte {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return body
	}

	usageData, ok := raw["usage"]
	if !ok || string(usageData) == "null" {
		return body
	}

	var usage aimodel.Usage
	if err := json.Unmarshal(usageData, &usage); err != nil {
		return body
	}

	// Determine model from response if available.
	model := extractModel(raw)
	pricing := pricingLookup(model)

	if pricing == nil {
		return body
	}

	// Calculate cost.
	nonCachedInput := usage.PromptTokens - usage.CacheReadTokens

	cost := float64(nonCachedInput)/1_000_000*pricing.InputPerMTokens +
		float64(usage.CompletionTokens)/1_000_000*pricing.OutputPerMTokens +
		float64(usage.CacheReadTokens)/1_000_000*pricing.CachePerMTokens

	costJSON, err := json.Marshal(cost)
	if err != nil {
		return body
	}

	raw["estimated_cost_usd"] = costJSON

	enriched, err := json.Marshal(raw)
	if err != nil {
		return body
	}

	return enriched
}

// extractModel attempts to get the model name from a JSON response.
func extractModel(raw map[string]json.RawMessage) string {
	if modelData, ok := raw["model"]; ok {
		var model string
		if err := json.Unmarshal(modelData, &model); err == nil {
			return model
		}
	}

	return ""
}

// enrichStreamResponse wraps the SSE stream to accumulate token counts
// and emit a final usage event.
func enrichStreamResponse(next http.Handler, w http.ResponseWriter, r *http.Request, pricingLookup func(string) *costtracker.Pricing) {
	rec := &streamRecorder{
		ResponseWriter: w,
		body:           &bytes.Buffer{},
		statusCode:     http.StatusOK,
	}

	next.ServeHTTP(rec, r)

	// If not a successful SSE response, just write through.
	if rec.statusCode != http.StatusOK {
		w.WriteHeader(rec.statusCode)
		_, _ = w.Write(rec.body.Bytes())

		return
	}

	// Parse SSE events, write them through, and accumulate usage.
	tracker := costtracker.New("", nil)

	var model string

	scanner := bufio.NewScanner(rec.body)

	var currentEvent string

	for scanner.Scan() {
		line := scanner.Text()

		if after, ok := strings.CutPrefix(line, "event: "); ok {
			currentEvent = after

			continue
		}

		if after, ok := strings.CutPrefix(line, "data: "); ok {
			data := after

			// Write the original event through.
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", currentEvent, data)

			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}

			// Accumulate token usage from llm_call_end events.
			if currentEvent == "llm_call_end" {
				var eventData struct {
					Data struct {
						Model            string `json:"model"`
						PromptTokens     int    `json:"prompt_tokens"`
						CompletionTokens int    `json:"completion_tokens"`
						CacheReadTokens  int    `json:"cache_read_tokens"`
					} `json:"data"`
				}
				if err := json.Unmarshal([]byte(data), &eventData); err == nil {
					if model == "" {
						model = eventData.Data.Model
					}

					tracker.Add(eventData.Data.PromptTokens, eventData.Data.CompletionTokens, eventData.Data.CacheReadTokens)
				}
			}

			currentEvent = ""

			continue
		}

		// Write through heartbeats and other lines.
		_, _ = fmt.Fprintf(w, "%s\n", line)

		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	// Re-create tracker with pricing to compute cost.
	pricing := pricingLookup(model)
	pricedTracker := costtracker.New(model, pricing)

	snap := tracker.Snapshot()
	pricedTracker.Add(snap.InputTokens, snap.OutputTokens, snap.CacheReadTokens)

	finalSnap := pricedTracker.Snapshot()

	usageData, err := json.Marshal(finalSnap)
	if err == nil {
		_, _ = fmt.Fprintf(w, "event: usage\ndata: %s\n\n", usageData)

		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

// responseRecorder captures the response body for sync responses.
type responseRecorder struct {
	http.ResponseWriter
	body       *bytes.Buffer
	statusCode int
	written    bool
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.written = true
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
}

// streamRecorder captures the full SSE stream for post-processing.
type streamRecorder struct {
	http.ResponseWriter
	body       *bytes.Buffer
	statusCode int
}

func (r *streamRecorder) WriteHeader(code int) {
	r.statusCode = code
}

func (r *streamRecorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
}

func (r *streamRecorder) Flush() {} // no-op during recording

// Hijack implements http.Hijacker for compatibility.
func (r *streamRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}

	return nil, nil, fmt.Errorf("httpapis: upstream ResponseWriter does not implement http.Hijacker")
}

func isJSONContent(h http.Header) bool {
	return strings.Contains(h.Get("Content-Type"), "application/json")
}
