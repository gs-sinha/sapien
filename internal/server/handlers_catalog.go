package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// handleOperationsList implements GET /v1/operations?q=&method=&service=&limit=&include_deprecated=.
// With q set it searches (engine.SearchAPI.Operations); otherwise it lists
// the catalog (engine.CatalogAPI.ListOperations). The two return different
// shapes: search results carry a score and match reasons.
func (s *Server) handleOperationsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	service := r.URL.Query().Get("service")

	if q != "" {
		opts := domain.SearchOptions{
			Service:           service,
			Method:            r.URL.Query().Get("method"),
			Limit:             queryInt(r, "limit", 0),
			IncludeDeprecated: queryBool(r, "include_deprecated", false),
		}
		out, err := engineFrom(r.Context()).Search().Operations(r.Context(), q, opts)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	out, err := engineFrom(r.Context()).Catalog().ListOperations(r.Context(), service)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleOperationResolve(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		writeError(w, errs.New(errs.Invalid, "missing required query parameter %q", "ref"))
		return
	}
	out, err := engineFrom(r.Context()).Catalog().ResolveOperation(r.Context(), ref)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleOperationGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := engineFrom(r.Context()).Catalog().GetOperation(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleOperationFields(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := engineFrom(r.Context()).Catalog().Fields(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSchemaGet(w http.ResponseWriter, r *http.Request) {
	service := chi.URLParam(r, "service")
	name := chi.URLParam(r, "name")
	out, err := engineFrom(r.Context()).Catalog().GetSchema(r.Context(), service, name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDocsList implements GET /v1/docs?q=&service=&limit=. With q set it
// searches doc sections (engine.SearchAPI.Docs); otherwise it lists whole
// docs (engine.CatalogAPI.ListDocs).
func (s *Server) handleDocsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	service := r.URL.Query().Get("service")

	if q != "" {
		opts := domain.SearchOptions{Service: service, Limit: queryInt(r, "limit", 0)}
		out, err := engineFrom(r.Context()).Search().Docs(r.Context(), q, opts)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	out, err := engineFrom(r.Context()).Catalog().ListDocs(r.Context(), service)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDocGet implements GET /v1/docs/{service}/*, where the trailing
// wildcard is the doc path (which may itself contain slashes, e.g.
// "docs/allocation.md", or a "contract#tag:name" fragment).
func (s *Server) handleDocGet(w http.ResponseWriter, r *http.Request) {
	service := chi.URLParam(r, "service")
	path := chi.URLParam(r, "*")
	out, err := engineFrom(r.Context()).Catalog().GetDoc(r.Context(), service, path)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
