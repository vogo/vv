package httpapis

import (
	"errors"
	"net/http"
	"strings"

	"github.com/vogo/vage/checkpoint"
	"github.com/vogo/vage/session"
	"github.com/vogo/vv/setup"
	"github.com/vogo/vv/skillevolve"
)

func skillEvolveOf(initResult *setup.InitResult) *skillevolve.Engine {
	if initResult == nil {
		return nil
	}
	return initResult.SkillEvolve
}

func mountSkillEvolveRoutes(mux *http.ServeMux, initResult *setup.InitResult) {
	eng := skillEvolveOf(initResult)
	if eng == nil {
		return
	}
	mux.HandleFunc("POST /v1/sessions/{id}/skill-extract", handleSkillExtract(eng))
	mux.HandleFunc("GET /v1/skill-proposals", handleSkillProposalList(eng))
	mux.HandleFunc("POST /v1/skill-proposals/{id}/approve", handleSkillProposalApprove(eng))
	mux.HandleFunc("POST /v1/skill-proposals/{id}/reject", handleSkillProposalReject(eng))
}

func handleSkillExtract(eng *skillevolve.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sid := strings.TrimSpace(r.PathValue("id"))
		if sid == "" || !session.IDPattern.MatchString(sid) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "bad_request", "message": "invalid session id",
			})
			return
		}
		p, err := eng.Extract(r.Context(), sid)
		if writeSkillEvolveErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

func handleSkillProposalList(eng *skillevolve.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := eng.List(r.Context())
		if writeSkillEvolveErr(w, err) {
			return
		}
		if list == nil {
			list = []skillevolve.Proposal{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"proposals": list})
	}
}

func handleSkillProposalApprove(eng *skillevolve.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "bad_request", "message": "invalid proposal id",
			})
			return
		}
		p, err := eng.Approve(r.Context(), id)
		if writeSkillEvolveErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

func handleSkillProposalReject(eng *skillevolve.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "bad_request", "message": "invalid proposal id",
			})
			return
		}
		p, err := eng.Reject(r.Context(), id)
		if writeSkillEvolveErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

func writeSkillEvolveErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var ne *skillevolve.ErrNotEligible
	if errors.As(err, &ne) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "not_eligible", "message": ne.Error(), "reason": ne.Eligibility.Reason,
		})
		return true
	}
	var np *skillevolve.ErrNotPending
	if errors.As(err, &np) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "not_pending", "message": np.Error(),
		})
		return true
	}
	var nf *skillevolve.ErrProposalNotFound
	if errors.As(err, &nf) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "not_found", "message": nf.Error(),
		})
		return true
	}
	var inv *skillevolve.ErrInvalidProposalID
	if errors.As(err, &inv) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "bad_request", "message": inv.Error(),
		})
		return true
	}
	var xf *skillevolve.ErrExtractFailed
	if errors.As(err, &xf) {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"code": "extract_failed", "message": xf.Error(),
		})
		return true
	}
	if errors.Is(err, checkpoint.ErrInvalidArgument) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "bad_request", "message": err.Error(),
		})
		return true
	}
	if errors.Is(err, checkpoint.ErrCheckpointNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "not_found", "message": err.Error(),
		})
		return true
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"code": "error", "message": err.Error(),
	})
	return true
}
