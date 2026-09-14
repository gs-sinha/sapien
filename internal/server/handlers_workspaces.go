package server

import (
	"net/http"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspaces"
)

// handleWorkspacesList implements GET /v1/workspaces: every workspace this
// daemon can serve, so a client can offer a switcher without knowing any
// paths in advance. A single-workspace daemon reports just its own, which
// keeps the route meaningful for an older embedding rather than empty.
func (s *Server) handleWorkspacesList(w http.ResponseWriter, r *http.Request) {
	if s.workspaces != nil {
		writeJSON(w, http.StatusOK, s.workspaces.List())
		return
	}

	var out []workspaces.Info
	if ws := s.engine.Workspace(); ws != nil {
		out = append(out, workspaces.Info{
			Dir:      ws.Dir,
			Name:     ws.Name,
			Primary:  true,
			Open:     true,
			Services: len(ws.Services),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleWorkspaceRegister implements POST /v1/workspaces: register a
// workspace directory so it shows up in the list, and open it now so the
// caller learns immediately whether it actually loads. It never creates a
// workspace -- `sapien init` does that.
func (s *Server) handleWorkspaceRegister(w http.ResponseWriter, r *http.Request) {
	var req registerWorkspaceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.Dir == "" {
		writeError(w, errs.New(errs.Invalid, "dir is required"))
		return
	}
	if s.workspaces == nil {
		writeError(w, errs.New(errs.Conflict, "this daemon serves a single workspace").
			WithHint("restart it with a build that supports multiple workspaces"))
		return
	}

	eng, err := s.workspaces.Register(req.Dir)
	if err != nil {
		writeError(w, err)
		return
	}

	var ws *domain.Workspace
	if ws = eng.Workspace(); ws == nil {
		writeError(w, errs.New(errs.Internal, "workspace %q opened without metadata", req.Dir))
		return
	}

	writeJSON(w, http.StatusOK, workspaces.Info{
		Dir:      ws.Dir,
		Name:     ws.Name,
		Primary:  ws.Dir == s.workspaces.Primary(),
		Open:     true,
		Services: len(ws.Services),
	})
}

// handleWorkspaceClose implements DELETE /v1/workspaces?dir=: close that
// workspace's engine on this daemon and release its lock. It does not
// unregister it (`sapien workspace forget` does that, and calls this), so
// a later request naming a still-registered workspace reopens it.
func (s *Server) handleWorkspaceClose(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		writeError(w, errs.New(errs.Invalid, "dir is required"))
		return
	}
	if s.workspaces == nil {
		writeError(w, errs.New(errs.Conflict, "this daemon serves a single workspace").
			WithHint("stop the daemon instead"))
		return
	}
	if err := s.workspaces.CloseOne(dir); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}
