package server

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

func (s *Server) handleRunFlowSource(w http.ResponseWriter, r *http.Request) {
	var req runFlowSourceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := engineFrom(r.Context()).Runner().RunFlowSource(r.Context(), req.YAML, req.Opts.toEngine())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRunCancel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := engineFrom(r.Context()).Runner().Cancel(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *Server) handleRunsList(w http.ResponseWriter, r *http.Request) {
	filter := domain.RunFilter{
		FlowID:    r.URL.Query().Get("flow"),
		Status:    domain.RunStatus(r.URL.Query().Get("status")),
		Operation: r.URL.Query().Get("operation"),
		Limit:     queryInt(r, "limit", 0),
		Offset:    queryInt(r, "offset", 0),
	}
	out, err := engineFrom(r.Context()).Runs().List(r.Context(), filter)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRunGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := engineFrom(r.Context()).Runs().Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRunStepGet implements GET /v1/runs/{id}/steps/{step}. step matches
// either a StepResult.StepID or its numeric Index; there is no dedicated
// engine.RunAPI method for this, so it filters the result of Runs().Get.
func (s *Server) handleRunStepGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	step := chi.URLParam(r, "step")

	run, err := engineFrom(r.Context()).Runs().Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}

	idx, isIndex := -1, false
	if n, convErr := strconv.Atoi(step); convErr == nil {
		idx, isIndex = n, true
	}

	for _, st := range run.Steps {
		if st.StepID == step || (isIndex && st.Index == idx) {
			writeJSON(w, http.StatusOK, st)
			return
		}
	}
	writeError(w, errs.New(errs.RunNotFound, "run %q has no step %q", id, step).WithDetail("id", id).WithDetail("step", step))
}

func (s *Server) handleRunPin(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req pinRunRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := engineFrom(r.Context()).Runs().Pin(r.Context(), id, req.Pinned); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *Server) handleRunsPurge(w http.ResponseWriter, r *http.Request) {
	var req purgeRunsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	removed, err := engineFrom(r.Context()).Runs().Purge(r.Context(), req.Keep)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, purgeRunsResponse{Removed: removed})
}
