package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gs-sinha/sapien/internal/diagnose"
)

// runHintsResponse is the wire shape of GET /v1/runs/{id}/hints.
type runHintsResponse struct {
	Hints []diagnose.Hint `json:"hints"`
}

// handleRunHints implements GET /v1/runs/{id}/hints: candidate
// explanations (internal/diagnose) for every failed or errored step of
// the run that carries an HTTP response with status >= 400. diagnose.Run
// returns nil for a run with no such step (most commonly a passed run),
// which this handler renders as an empty "hints" array rather than null,
// so a UI client never has to special-case the absent-vs-empty
// distinction. An unknown run id is 404 (from Runs().Get, unchanged).
func (s *Server) handleRunHints(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	run, err := engineFrom(r.Context()).Runs().Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}

	hints := diagnose.Run(r.Context(), s.engine, run)
	if hints == nil {
		hints = []diagnose.Hint{}
	}
	writeJSON(w, http.StatusOK, runHintsResponse{Hints: hints})
}
