package httpapis

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/interrupt"
	"github.com/vogo/vv/setup"
)

func interruptStoreOf(initResult *setup.InitResult) interrupt.Store {
	if initResult == nil {
		return nil
	}
	return initResult.InterruptStore
}

type interruptListResponse struct {
	Interrupts []*interrupt.Meta `json:"interrupts"`
}

type interruptDecisionsRequest struct {
	Decisions []interrupt.Decision `json:"decisions"`
}

type interruptDecisionsResponse struct {
	ID        string   `json:"id"`
	Status    string   `json:"status"`
	Pending   []string `json:"pending"`
	Committed []string `json:"committed"`
	Ready     bool     `json:"ready"`
}

func handleListInterrupts(store interrupt.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sid := strings.TrimSpace(r.PathValue("id"))
		if sid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "bad_request", "message": "session id is empty",
			})
			return
		}

		list, err := store.List(r.Context(), sid)
		if writeInterruptErr(w, err) {
			return
		}
		if list == nil {
			list = []*interrupt.Meta{}
		}
		writeJSON(w, http.StatusOK, interruptListResponse{Interrupts: list})
	}
}

func handleSubmitInterruptDecisions(store interrupt.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "bad_request", "message": "interrupt id is empty",
			})
			return
		}

		var req interruptDecisionsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "bad_request", "message": "invalid request body",
			})
			return
		}

		rec, committed, err := store.SubmitDecisions(r.Context(), id, req.Decisions)
		if rec != nil {
			writeJSON(w, statusForInterruptSubmit(err), interruptDecisionsResponse{
				ID:        rec.ID,
				Status:    string(rec.Status),
				Pending:   rec.Pending,
				Committed: committed,
				Ready:     rec.Status == interrupt.StatusReady || rec.Status == interrupt.StatusResuming,
			})
			return
		}
		if writeInterruptErr(w, err) {
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "error", "message": "submit decisions returned no record",
		})
	}
}

func statusForInterruptSubmit(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, interrupt.ErrDecisionConflict) || errors.Is(err, interrupt.ErrAlreadyCompleted) {
		return http.StatusConflict
	}
	if errors.Is(err, interrupt.ErrUnknownToolCall) || errors.Is(err, interrupt.ErrInvalidArgument) {
		return http.StatusBadRequest
	}
	if errors.Is(err, interrupt.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func handleResumeInterrupt(store interrupt.Store, initResult *setup.InitResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "bad_request", "message": "interrupt id is empty",
			})
			return
		}

		rec, err := store.Get(r.Context(), id)
		if writeInterruptErr(w, err) {
			return
		}

		if initResult == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"code":    "interrupt_disabled",
				"message": "interrupt resume is not configured",
			})
			return
		}

		a, err := initResult.ResumeAgent(rec.AgentID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"code": "agent_not_found", "message": err.Error(),
			})
			return
		}

		ta, ok := a.(*taskagent.Agent)
		if !ok {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"code":    "agent_not_resumable",
				"message": "interrupt references an agent kind that does not support ResumeInterrupt",
			})
			return
		}

		resp, err := ta.ResumeInterrupt(r.Context(), schema.ResumeInterruptRequest{InterruptID: id})
		if writeInterruptErr(w, err) {
			return
		}

		out := toResumeResponse(rec.SessionID, rec.AgentID, resp)
		if resp != nil {
			out.Interrupt = resp.Interrupt
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func writeInterruptErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, interrupt.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "not_found", "message": err.Error(),
		})
	case errors.Is(err, interrupt.ErrInvalidArgument),
		errors.Is(err, interrupt.ErrUnknownToolCall):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "bad_request", "message": err.Error(),
		})
	case errors.Is(err, taskagent.ErrInterruptPolicyDrift):
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "policy_drift", "message": err.Error(),
		})
	case errors.Is(err, interrupt.ErrDecisionConflict),
		errors.Is(err, interrupt.ErrAlreadyCompleted),
		errors.Is(err, interrupt.ErrLeaseHeld),
		errors.Is(err, taskagent.ErrInterruptAgentMismatch):
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "conflict", "message": err.Error(),
		})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "error", "message": err.Error(),
		})
	}
	return true
}

func deleteSessionInterrupts(store interrupt.Store, sessionID string, r *http.Request) {
	if store == nil || sessionID == "" {
		return
	}
	list, err := store.List(r.Context(), sessionID)
	if err != nil {
		return
	}
	for _, meta := range list {
		_ = store.Delete(r.Context(), meta.ID)
	}
}
