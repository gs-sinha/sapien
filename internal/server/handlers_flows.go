package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/folder"
)

// handleFlowsList implements GET /v1/flows?q=&folder= (PLAN §34f item 4):
// folder restricts results to that folder and everything below it. Folder
// is not a catalog column (item 6: derived from the path, no DB column), so
// it is applied here, in Go, after Flows().List has already done its own
// id/name/tag/folder substring match on q.
func (s *Server) handleFlowsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	out, err := engineFrom(r.Context()).Flows().List(r.Context(), q)
	if err != nil {
		writeError(w, err)
		return
	}
	if prefix := r.URL.Query().Get("folder"); prefix != "" {
		out = filterFlowsByFolder(out, prefix)
	}
	writeJSON(w, http.StatusOK, out)
}

// filterFlowsByFolder keeps only the flows in prefix's folder or below it
// (PLAN §34f item 4's prefix semantics: internal/folder.HasPrefix).
func filterFlowsByFolder(flows []domain.FlowSummary, prefix string) []domain.FlowSummary {
	norm := folder.Normalize(prefix)
	out := flows[:0]
	for _, f := range flows {
		if folder.HasPrefix(f.Folder, norm) {
			out = append(out, f)
		}
	}
	return out
}

// createFlowRequest is POST /v1/flows' body: the YAML plus where it goes.
// Path is relative to the chosen tier's flows directory; Folder places it
// at <folder>/<id>.flow.yaml within that directory instead (PLAN §34f item
// 4: mutually exclusive with Path); owner_kind names the tier (local,
// workspace, service) and owner_id the service for the service tier.
type createFlowRequest struct {
	YAML      string `json:"yaml"`
	Path      string `json:"path,omitempty"`
	Folder    string `json:"folder,omitempty"`
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
	if req.OwnerKind == "" && req.Folder == "" {
		// The engine's CreateIn defaults an empty owner to the local tier,
		// but on the wire an absent owner_kind means an older client: a
		// remote CLI, UI build or MCP bridge that predates tiers and has
		// only ever known <workspace>/flows. Keeping Create here means an
		// upgraded daemon does not quietly start filing those callers' flows
		// somewhere the team never sees; a client that wants local says so.
		out, err = flows.Create(r.Context(), req.YAML, req.Path)
	} else {
		// req.Folder needs CreateIn regardless of owner_kind (Create's
		// shorthand has no folder parameter): flows.Create is exactly
		// CreateIn with owner_kind workspace, so an absent owner_kind here
		// defaults the same way.
		ownerKind := req.OwnerKind
		if ownerKind == "" {
			ownerKind = domain.FlowOwnerWorkspace
		}
		out, err = flows.CreateIn(r.Context(), req.YAML, engine.CreateFlowOptions{
			Path: req.Path, Folder: req.Folder, OwnerKind: ownerKind, OwnerID: req.OwnerID,
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

// moveFlowRequest is POST /v1/flows/{id}/move's body (PLAN §34f item 6):
// the folder to move the flow to within its current tier. The client-side
// twin is internal/engine/remote/flows.go's own moveFlowRequest.
type moveFlowRequest struct {
	Folder string `json:"folder"`
}

// handleFlowMove implements POST /v1/flows/{id}/move: places the flow at
// folder within its current tier's flows directory, keeping tier and file
// name. Every refusal (read-only service tier, a taken destination) is the
// engine's own error, passed through writeError unchanged.
func (s *Server) handleFlowMove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req moveFlowRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := engineFrom(r.Context()).Flows().Move(r.Context(), id, req.Folder)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// commitFlowRequest is POST /v1/flows/{id}/commit's body (the client-side
// twin, internal/engine/remote/flows.go's commitFlowRequest, sends the
// same shape): message "" picks the engine's own default.
type commitFlowRequest struct {
	Message string `json:"message,omitempty"`
}

func (s *Server) handleFlowCommit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req commitFlowRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := engineFrom(r.Context()).Flows().Commit(r.Context(), id, req.Message)
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
