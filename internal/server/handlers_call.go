package server

import (
	"net/http"

	"github.com/gs-sinha/sapien/internal/engine"
)

func (s *Server) handleCall(w http.ResponseWriter, r *http.Request) {
	var req engine.CallRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := engineFrom(r.Context()).Runner().Call(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
