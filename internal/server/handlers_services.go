package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/gs-sinha/sapien/internal/errs"
)

func (s *Server) handleServicesList(w http.ResponseWriter, r *http.Request) {
	out, err := engineFrom(r.Context()).Services().List(r.Context())
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
	out, err := engineFrom(r.Context()).Services().Add(r.Context(), req.Name, req.Source)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleServiceGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := engineFrom(r.Context()).Services().Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleServiceRemove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := engineFrom(r.Context()).Services().Remove(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *Server) handleServicesSyncAll(w http.ResponseWriter, r *http.Request) {
	out, err := engineFrom(r.Context()).Services().Sync(r.Context(), "")
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleServiceSync(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := engineFrom(r.Context()).Services().Sync(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleServicesReindex(w http.ResponseWriter, r *http.Request) {
	if err := engineFrom(r.Context()).Services().Reindex(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

// bindServiceRequest is PUT /v1/services/{id}/binding's body: the local
// checkout this machine should read the service from.
type bindServiceRequest struct {
	Path string `json:"path"`
}

func (s *Server) handleServiceBindingGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := engineFrom(r.Context()).Services().Binding(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleServiceBind(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req bindServiceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	// An empty path is rejected here rather than left to the engine: a
	// client that forgot the field would otherwise record an override to
	// nowhere in sapien.workspace.local.yaml and the service would stop
	// resolving on this machine until someone found and deleted it.
	if strings.TrimSpace(req.Path) == "" {
		writeError(w, errs.New(errs.Invalid, "path is required").
			WithHint("pass the absolute path of the local checkout to read the service from"))
		return
	}
	out, err := engineFrom(r.Context()).Services().Bind(r.Context(), id, req.Path)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleServiceUnbind(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := engineFrom(r.Context()).Services().Unbind(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
