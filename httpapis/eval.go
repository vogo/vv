package httpapis

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	vageeval "github.com/vogo/vage/eval"
	"github.com/vogo/vv/configs"
	vveval "github.com/vogo/vv/eval"
)

type evalRunRequest struct {
	Cases []json.RawMessage `json:"cases"`
}

// handleEvalRun runs the configured evaluator over the posted cases and
// returns a vage/eval.EvalReport. Only mounted when cfg.Eval.Enabled is
// true — disabled means the route is absent, not a 403/404 toggle.
func handleEvalRun(cfg *configs.Config, dispatcher agent.Agent, llm aimodel.ChatCompleter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req evalRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": "invalid json body"})

			return
		}

		if len(req.Cases) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": "cases must not be empty"})

			return
		}

		cases := make([]*vageeval.EvalCase, 0, len(req.Cases))

		var errorCases []*vageeval.EvalResult

		for i, raw := range req.Cases {
			c, err := vveval.DecodeCaseLine(raw)
			if err != nil {
				errorCases = append(errorCases, &vageeval.EvalResult{
					CaseID: caseIDFallback(raw, i),
					Error:  err.Error(),
				})

				continue
			}

			cases = append(cases, c)
		}

		evaluator, err := vveval.Build(cfg.Eval, llm, cfg.LLM.Model)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "error", "message": err.Error()})

			return
		}

		report, err := vveval.RunBatch(r.Context(), dispatcher.Run, evaluator, cases, cfg.Eval)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "error", "message": err.Error()})

			return
		}

		if len(errorCases) > 0 {
			report.TotalCases += len(errorCases)
			report.ErrorCases += len(errorCases)
			report.Results = append(report.Results, errorCases...)
		}

		writeJSON(w, http.StatusOK, report)
	}
}

// caseIDFallback extracts the id from a raw case for error reporting
// when the full record failed to decode. Returns a synthetic "case-N"
// tag as the last resort so every error result has a stable identifier.
func caseIDFallback(raw []byte, index int) string {
	var partial struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal(raw, &partial); err == nil && partial.ID != "" {
		return partial.ID
	}

	return "case-" + strconv.Itoa(index)
}
