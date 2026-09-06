package server

import (
	"net/http"

	"github.com/gs-sinha/sapien/internal/domain"
)

func (s *Server) handleContextBuild(w http.ResponseWriter, r *http.Request) {
	var req domain.ContextRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := s.engine.Context().Build(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
