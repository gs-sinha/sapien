package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/example"
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

// handleOperationExample answers "what does a call to this look like?" with a
// payload rather than a schema: the best available of a verified example, a
// saved one, the contract's own `example:`, and one synthesized from the
// request schema (see internal/example.Resolve). ?fields=all instead
// synthesizes every field the schema declares, for a human who wants the
// whole shape in front of them to delete from.
func (s *Server) handleOperationExample(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	eng := engineFrom(r.Context())
	op, err := eng.Catalog().GetOperation(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	if r.URL.Query().Get("fields") == "all" {
		// "Whole shape": synthesize every field from the schema. Answering
		// this with a saved or contract example would hand back the payload
		// the caller is already looking at.
		writeJSON(w, http.StatusOK, example.Synthesize(op, true))
		return
	}
	// Saved examples are supporting material: an example store that cannot
	// be read degrades to the contract/schema answer rather than failing a
	// request the caller made about the operation.
	saved, _ := eng.Examples().ForOperations(r.Context(), []string{op.ID}, 5)
	writeJSON(w, http.StatusOK, example.Resolve(op, saved))
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
