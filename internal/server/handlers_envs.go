package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleEnvironmentsList(w http.ResponseWriter, r *http.Request) {
	out, err := s.engine.Envs().List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleEnvironmentGet(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	out, err := s.engine.Envs().Get(r.Context(), name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleEnvironmentDefaultGet(w http.ResponseWriter, r *http.Request) {
	name, err := s.engine.Envs().Default(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, defaultEnvironmentResponse{Name: name})
}

func (s *Server) handleEnvironmentDefaultSet(w http.ResponseWriter, r *http.Request) {
	var req defaultEnvironmentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := s.engine.Envs().SetDefault(r.Context(), req.Name); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *Server) handleSecretsList(w http.ResponseWriter, r *http.Request) {
	out, err := s.engine.Envs().ListSecrets(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSecretSet(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var req setSecretRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := s.engine.Envs().SetSecret(r.Context(), name, req.Value); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *Server) handleSecretDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if err := s.engine.Envs().DeleteSecret(r.Context(), name); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}
