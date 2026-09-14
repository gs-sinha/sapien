package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

func (s *Server) handleFlowsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	out, err := engineFrom(r.Context()).Flows().List(r.Context(), q)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// createFlowRequest is POST /v1/flows' body: the YAML plus where it goes.
// Path is relative to the chosen tier's flows directory; owner_kind names
// the tier (local, workspace, service) and owner_id the service for the
// service tier.
type createFlowRequest struct {
	YAML      string `json:"yaml"`
	Path      string `json:"path,omitempty"`
	OwnerKind string `json:"owner_kind,omitempty"`
	OwnerID   string `json:"owner_id,omitempty"`
}

func (s *Server) handleFlowCreate(w http.ResponseWriter, r *http.Request) {
	var req createFlowRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	flows := engineFrom(r.Context()).Flows()
	var out *domain.Flow
	var err error
	if req.OwnerKind == "" {
		// The engine's CreateIn defaults an empty owner to the local tier,
		// but on the wire an absent owner_kind means an older client: a
		// remote CLI, UI build or MCP bridge that predates tiers and has
		// only ever known <workspace>/flows. Keeping Create here means an
		// upgraded daemon does not quietly start filing those callers' flows
		// somewhere the team never sees; a client that wants local says so.
		out, err = flows.Create(r.Context(), req.YAML, req.Path)
	} else {
		out, err = flows.CreateIn(r.Context(), req.YAML, engine.CreateFlowOptions{
			Path: req.Path, OwnerKind: req.OwnerKind, OwnerID: req.OwnerID,
		})
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// rescopeFlowRequest is POST /v1/flows/{id}/rescope's body: the tier to
// move the flow to, and the service when that tier is service. Commit and
// Message mirror engine.RescopeOptions (internal/engine/remote/flows.go's
// rescopeFlowRequest is the client-side twin of this same wire shape):
// Commit records the moved file in the workspace repository with one
// commit, only for a promotion to the workspace tier, and never pushes;
// Message overrides the default commit message.
type rescopeFlowRequest struct {
	OwnerKind string `json:"owner_kind"`
	OwnerID   string `json:"owner_id,omitempty"`
	Commit    bool   `json:"commit,omitempty"`
	Message   string `json:"message,omitempty"`
}

func (s *Server) handleFlowRescope(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req rescopeFlowRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.OwnerKind == "" {
		writeError(w, errs.New(errs.Invalid, "owner_kind is required").
			WithHint("pass owner_kind local, workspace, or service (with owner_id naming the service)"))
		return
	}
	out, err := engineFrom(r.Context()).Flows().RescopeWith(r.Context(), id, req.OwnerKind, req.OwnerID, engine.RescopeOptions{
		Commit: req.Commit, Message: req.Message,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFlowGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := engineFrom(r.Context()).Flows().Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFlowUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req flowYAMLRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := engineFrom(r.Context()).Flows().Update(r.Context(), id, req.YAML)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFlowDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := engineFrom(r.Context()).Flows().Delete(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

func (s *Server) handleFlowValidate(w http.ResponseWriter, r *http.Request) {
	var req flowYAMLRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := engineFrom(r.Context()).Flows().Validate(r.Context(), req.YAML)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFlowParse(w http.ResponseWriter, r *http.Request) {
	var req flowYAMLRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := engineFrom(r.Context()).Flows().Parse(r.Context(), req.YAML)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFlowReference(w http.ResponseWriter, r *http.Request) {
	topic := r.URL.Query().Get("topic")
	out, err := engineFrom(r.Context()).Flows().Reference(r.Context(), topic)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleFlowRun implements POST /v1/flows/{id}/run. Note: unlike PLAN §22's
// compact sketch ("→ {run_id}"), this returns the full domain.Run, because
// engine.RunnerAPI.RunFlow is a synchronous, blocking call in the fixed
// Engine interface -- it has no separate async/poll path, so there is
// nothing to return but the finished run. Callers that want to watch
// progress as it happens should subscribe to GET /v1/events, since
// RunOptions.Observer cannot cross the wire (see runOptionsWire).
func (s *Server) handleFlowRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req runOptionsWire
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := engineFrom(r.Context()).Runner().RunFlow(r.Context(), id, req.toEngine())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
