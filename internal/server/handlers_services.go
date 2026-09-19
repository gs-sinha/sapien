package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/gs-sinha/sapien/internal/engine"
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

// bindServiceRequest is PUT /v1/services/{id}/binding's (and PUT
// /v1/services/binding's) body: the local checkout this machine should
// read the service from. Force overrides the origin-vs-team-repository
// check (and the no-API-package check) that BindWith otherwise applies.
type bindServiceRequest struct {
	Path  string `json:"path"`
	Force bool   `json:"force,omitempty"`
}

// requireBindPath rejects an empty path here rather than leaving it to the
// engine: a client that forgot the field would otherwise record an
// override to nowhere in sapien.workspace.local.yaml and the service would
// stop resolving on this machine until someone found and deleted it.
func requireBindPath(w http.ResponseWriter, path string) bool {
	if strings.TrimSpace(path) != "" {
		return true
	}
	writeError(w, errs.New(errs.Invalid, "path is required").
		WithHint("pass the absolute path of the local checkout to read the service from"))
	return false
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
	if !requireBindPath(w, req.Path) {
		return
	}
	out, err := engineFrom(r.Context()).Services().BindWith(r.Context(), id, req.Path, engine.BindOptions{Force: req.Force})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleServiceBindByCheckout is PUT /v1/services/binding: no {id} in the
// URL, so the service is inferred from the checkout's origin (the one
// registered team source naming that repository).
func (s *Server) handleServiceBindByCheckout(w http.ResponseWriter, r *http.Request) {
	var req bindServiceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if !requireBindPath(w, req.Path) {
		return
	}
	out, err := engineFrom(r.Context()).Services().BindWith(r.Context(), "", req.Path, engine.BindOptions{Force: req.Force})
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

// setRefRequest is PUT /v1/services/{id}/ref's body (PLAN §34f item 2).
// Scope "" defaults to domain.RefScopeLocal, matching the engine's own
// default.
type setRefRequest struct {
	Ref   string `json:"ref"`
	Scope string `json:"scope,omitempty"`
}

// handleServiceSetRef is PUT /v1/services/{id}/ref: switches a git-sourced
// service's ref, either just for this machine (sapien.workspace.local.yaml)
// or for the team (source.ref in the committed sapien.workspace.yaml). The
// engine refuses (errs.Invalid, mapped to 400) a non-git service or a ref
// the remote does not have.
func (s *Server) handleServiceSetRef(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req setRefRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(req.Ref) == "" {
		writeError(w, errs.New(errs.Invalid, "ref is required").
			WithHint("pass the branch, tag, or commit to switch to"))
		return
	}
	out, err := engineFrom(r.Context()).Services().SetRef(r.Context(), id, req.Ref, req.Scope)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleServiceClearRef is DELETE /v1/services/{id}/ref: clears this
// machine's local ref override. The engine refuses (errs.Invalid, mapped to
// 400) when there is none to clear.
func (s *Server) handleServiceClearRef(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := engineFrom(r.Context()).Services().ClearRef(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleServiceBranches is GET /v1/services/{id}/branches: a git-sourced
// service's branches and tags from `ls-remote`.
func (s *Server) handleServiceBranches(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := engineFrom(r.Context()).Services().Branches(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleServiceBrowseCheckouts is GET /v1/services/{id}/checkouts?path=: a
// daemon-side directory picker, because a web page cannot learn an
// absolute path from a file dialog. A missing path lists the home
// directory (engine.ServiceAPI.BrowseCheckouts's "" case).
func (s *Server) handleServiceBrowseCheckouts(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	dir := r.URL.Query().Get("path")
	out, err := engineFrom(r.Context()).Services().BrowseCheckouts(r.Context(), id, dir)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// addFromCheckoutRequest is POST /v1/services/from-checkout's body: Name
// empty derives the name the way addService does; Ref pins the branch the
// new team source tracks; Force and AllowSubdir relax AddFromCheckout's
// validation exactly as engine.AddFromCheckoutOptions documents.
type addFromCheckoutRequest struct {
	Name        string `json:"name,omitempty"`
	Path        string `json:"path"`
	Ref         string `json:"ref,omitempty"`
	Force       bool   `json:"force,omitempty"`
	AllowSubdir bool   `json:"allow_subdir,omitempty"`
}

// handleServiceAddFromCheckout is POST /v1/services/from-checkout:
// registers the repository a local checkout was cloned from as a team git
// source and binds the checkout on this machine in one call, so a new hire
// gets a source every clone can read while this machine reads the working
// copy at once.
func (s *Server) handleServiceAddFromCheckout(w http.ResponseWriter, r *http.Request) {
	var req addFromCheckoutRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		writeError(w, errs.New(errs.Invalid, "path is required").
			WithHint("pass the absolute path of the local checkout to register and bind"))
		return
	}
	out, err := engineFrom(r.Context()).Services().AddFromCheckout(r.Context(), req.Name, req.Path, engine.AddFromCheckoutOptions{
		Ref: req.Ref, Force: req.Force, AllowSubdir: req.AllowSubdir,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}
