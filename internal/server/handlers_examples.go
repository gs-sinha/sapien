package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// exampleQueryFromRequest builds a domain.ExampleQuery from a List request's
// query string (PLAN §34b): GET /v1/examples?operation=&service=&tag=&text=&limit=&folder=.
// folder restricts results to that folder and everything below it (PLAN
// §34f item 4).
func exampleQueryFromRequest(r *http.Request) domain.ExampleQuery {
	q := r.URL.Query()
	return domain.ExampleQuery{
		Operation: q.Get("operation"),
		Service:   q.Get("service"),
		Tag:       q.Get("tag"),
		Text:      q.Get("text"),
		Limit:     queryInt(r, "limit", 0),
		Folder:    q.Get("folder"),
	}
}

func (s *Server) handleExamplesList(w http.ResponseWriter, r *http.Request) {
	out, err := engineFrom(r.Context()).Examples().List(r.Context(), exampleQueryFromRequest(r))
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
	out, err := engineFrom(r.Context()).Examples().Create(r.Context(), ex)
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
	out, err := engineFrom(r.Context()).Examples().ForOperations(r.Context(), operationIDs, limit)
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
	out, err := engineFrom(r.Context()).Examples().FromRun(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleExamplesReindex(w http.ResponseWriter, r *http.Request) {
	if err := engineFrom(r.Context()).Examples().Reindex(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *Server) handleExampleGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := engineFrom(r.Context()).Examples().Get(r.Context(), id)
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
	out, err := engineFrom(r.Context()).Examples().Update(r.Context(), ex)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleExampleDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := engineFrom(r.Context()).Examples().Delete(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

// handleExampleMove implements POST /v1/examples/{id}/move: with tier,
// places a workspace-scope example's file in another tier (local or
// workspace), keeping its id, scope, and folder; with folder, places it in
// another folder within its current directory, keeping its scope and tier
// (PLAN §34f item 6). Neither present is rejected before the engine is
// asked (moveTierRequest is declared in handlers_memories.go, shared by
// both routes' identical body shape).
func (s *Server) handleExampleMove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req moveTierRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	examples := engineFrom(r.Context()).Examples()
	var out *domain.SavedExample
	var err error
	switch {
	case req.Folder != nil:
		out, err = examples.MoveFolder(r.Context(), id, *req.Folder)
	case req.Tier != "":
		out, err = examples.Move(r.Context(), id, req.Tier)
	default:
		writeError(w, errs.New(errs.Invalid, "tier or folder is required").
			WithHint("pass tier (local or workspace) or folder"))
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleExampleCommit implements POST /v1/examples/{id}/commit: records a
// workspace-tier example's file in the workspace repository with one
// commit, never a push (commitMessageRequest is declared in
// handlers_memories.go, shared by both routes' identical body shape).
func (s *Server) handleExampleCommit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req commitMessageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := engineFrom(r.Context()).Examples().Commit(r.Context(), id, req.Message)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
