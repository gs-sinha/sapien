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
	out, err := engineFrom(r.Context()).Context().Build(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
