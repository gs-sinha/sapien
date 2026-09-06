package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

func memoryQueryFromRequest(r *http.Request) domain.MemoryQuery {
	q := r.URL.Query()
	return domain.MemoryQuery{
		Text:      q.Get("q"),
		Scope:     domain.MemoryScope(q.Get("scope")),
		Type:      domain.MemoryType(q.Get("type")),
		Service:   q.Get("service"),
		Operation: q.Get("op"),
		Flow:      q.Get("flow"),
		Limit:     queryInt(r, "limit", 0),
		MinScore:  queryFloat(r, "min_score", 0),
	}
}

func (s *Server) handleMemoriesList(w http.ResponseWriter, r *http.Request) {
	out, err := engineFrom(r.Context()).Memories().List(r.Context(), memoryQueryFromRequest(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleMemoryCreate(w http.ResponseWriter, r *http.Request) {
	var mem domain.Memory
	if err := decodeJSON(r, &mem); err != nil {
		writeError(w, err)
		return
	}
	out, err := engineFrom(r.Context()).Memories().Create(r.Context(), mem)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleMemoriesSearch(w http.ResponseWriter, r *http.Request) {
	out, err := engineFrom(r.Context()).Memories().Search(r.Context(), memoryQueryFromRequest(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleMemoriesRelevant(w http.ResponseWriter, r *http.Request) {
	var req relevantMemoriesRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := engineFrom(r.Context()).Memories().Relevant(r.Context(), req.Subjects, req.Limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleMemoriesReindex(w http.ResponseWriter, r *http.Request) {
	if err := engineFrom(r.Context()).Memories().Reindex(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *Server) handleMemoryGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := engineFrom(r.Context()).Memories().Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleMemoryUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var mem domain.Memory
	if err := decodeJSON(r, &mem); err != nil {
		writeError(w, err)
		return
	}
	if mem.ID == "" {
		mem.ID = id
	} else if mem.ID != id {
		writeError(w, errs.New(errs.Invalid, "body id %q does not match path id %q", mem.ID, id))
		return
	}
	out, err := engineFrom(r.Context()).Memories().Update(r.Context(), mem)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := engineFrom(r.Context()).Memories().Delete(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *Server) handleMemoryPromotion(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := engineFrom(r.Context()).Memories().PromotionTarget(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
