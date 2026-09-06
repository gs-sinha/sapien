package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleServicesList(w http.ResponseWriter, r *http.Request) {
	out, err := s.engine.Services().List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleServiceAdd(w http.ResponseWriter, r *http.Request) {
	var req addServiceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := s.engine.Services().Add(r.Context(), req.Name, req.Source)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleServiceGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := s.engine.Services().Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleServiceRemove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.engine.Services().Remove(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *Server) handleServicesSyncAll(w http.ResponseWriter, r *http.Request) {
	out, err := s.engine.Services().Sync(r.Context(), "")
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleServiceSync(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := s.engine.Services().Sync(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleServicesReindex(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.Services().Reindex(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}
