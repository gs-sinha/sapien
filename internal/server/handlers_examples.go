package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
	"github.com/growsimplee/sapien/internal/errs"
)

// exampleQueryFromRequest builds a domain.ExampleQuery from a List request's
// query string (PLAN §34b): GET /v1/examples?operation=&service=&tag=&text=&limit=.
func exampleQueryFromRequest(r *http.Request) domain.ExampleQuery {
	q := r.URL.Query()
	return domain.ExampleQuery{
		Operation: q.Get("operation"),
		Service:   q.Get("service"),
		Tag:       q.Get("tag"),
		Text:      q.Get("text"),
		Limit:     queryInt(r, "limit", 0),
	}
}

func (s *Server) handleExamplesList(w http.ResponseWriter, r *http.Request) {
	out, err := s.engine.Examples().List(r.Context(), exampleQueryFromRequest(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleExampleCreate(w http.ResponseWriter, r *http.Request) {
	var ex domain.SavedExample
	if err := decodeJSON(r, &ex); err != nil {
		writeError(w, err)
		return
	}
	out, err := s.engine.Examples().Create(r.Context(), ex)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// handleExamplesForOperations serves GET /v1/examples/for-operations, whose
// operation IDs travel as a repeated "op" query parameter (op=a&op=b), the
// same convention as internal/engine/remote's client.
func (s *Server) handleExamplesForOperations(w http.ResponseWriter, r *http.Request) {
	operationIDs := r.URL.Query()["op"]
	limit := queryInt(r, "limit", 0)
	out, err := s.engine.Examples().ForOperations(r.Context(), operationIDs, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleExampleFromRun serves POST /v1/examples/from-run. engine.ExampleFromRun
// has no func-typed field (unlike engine.RunOptions), so it decodes directly
// with no wire wrapper needed.
func (s *Server) handleExampleFromRun(w http.ResponseWriter, r *http.Request) {
	var req engine.ExampleFromRun
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := s.engine.Examples().FromRun(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleExamplesReindex(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.Examples().Reindex(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *Server) handleExampleGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := s.engine.Examples().Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleExampleUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var ex domain.SavedExample
	if err := decodeJSON(r, &ex); err != nil {
		writeError(w, err)
		return
	}
	if ex.ID == "" {
		ex.ID = id
	} else if ex.ID != id {
		writeError(w, errs.New(errs.Invalid, "body id %q does not match path id %q", ex.ID, id))
		return
	}
	out, err := s.engine.Examples().Update(r.Context(), ex)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleExampleDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.engine.Examples().Delete(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}
