package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleFlowsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	out, err := s.engine.Flows().List(r.Context(), q)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFlowCreate(w http.ResponseWriter, r *http.Request) {
	var req flowYAMLRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := s.engine.Flows().Create(r.Context(), req.YAML, req.Path)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleFlowGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := s.engine.Flows().Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFlowUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req flowYAMLRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := s.engine.Flows().Update(r.Context(), id, req.YAML)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFlowDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.engine.Flows().Delete(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *Server) handleFlowValidate(w http.ResponseWriter, r *http.Request) {
	var req flowYAMLRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := s.engine.Flows().Validate(r.Context(), req.YAML)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFlowParse(w http.ResponseWriter, r *http.Request) {
	var req flowYAMLRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := s.engine.Flows().Parse(r.Context(), req.YAML)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFlowReference(w http.ResponseWriter, r *http.Request) {
	topic := r.URL.Query().Get("topic")
	out, err := s.engine.Flows().Reference(r.Context(), topic)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleFlowRun implements POST /v1/flows/{id}/run. Note: unlike PLAN §22's
// compact sketch ("→ {run_id}"), this returns the full domain.Run, because
// engine.RunnerAPI.RunFlow is a synchronous, blocking call in the fixed
// Engine interface -- it has no separate async/poll path, so there is
// nothing to return but the finished run. Callers that want to watch
// progress as it happens should subscribe to GET /v1/events, since
// RunOptions.Observer cannot cross the wire (see runOptionsWire).
func (s *Server) handleFlowRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req runOptionsWire
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := s.engine.Runner().RunFlow(r.Context(), id, req.toEngine())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
