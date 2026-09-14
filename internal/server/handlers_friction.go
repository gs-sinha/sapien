package server

import (
	"net/http"
	"runtime"

	"github.com/go-chi/chi/v5"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/friction"
)

// Friction reports are per machine, not per workspace: an agent files one
// about Sapien itself, from whichever workspace it happens to be in, and a
// human reviews the queue as a whole. So these handlers read the store
// directly rather than going through the request's workspace engine, and
// they are the UI's counterpart to `sapien friction` (internal/cli). The
// one workspace-dependent input, which repository and category `send`
// posts to, comes from the engine-level config resolved against the
// request's workspace, so a workspace config can steer its reports.

// frictionStore opens the per-machine store; SAPIEN_FRICTION_DIR overrides
// the location, as it does for the CLI and the MCP tool.
func frictionStore() *friction.Store { return friction.New("") }

// handleFrictionList implements GET /v1/friction: every report, newest
// first, all statuses.
func (s *Server) handleFrictionList(w http.ResponseWriter, r *http.Request) {
	out, err := frictionStore().List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	if out == nil {
		out = []friction.Report{}
	}
	writeJSON(w, http.StatusOK, out)
}

// frictionPreview is GET /v1/friction/{id}/preview's answer: the exact
// Discussion that send would create, and where. The human sees this before
// anything goes public.
type frictionPreview struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Repo     string `json:"repo"`
	Category string `json:"category"`
}

func (s *Server) handleFrictionPreview(w http.ResponseWriter, r *http.Request) {
	rep, err := frictionStore().Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	cfg, err := s.frictionConfig(r)
	if err != nil {
		writeError(w, err)
		return
	}
	title, body := friction.Render(*rep, friction.Host{OS: runtime.GOOS})
	writeJSON(w, http.StatusOK, frictionPreview{Title: title, Body: body, Repo: cfg.Friction.Repo, Category: cfg.Friction.Category})
}

// handleFrictionSend implements POST /v1/friction/{id}/send: publish one
// report as a GitHub Discussion through the gh CLI and record the URL. The
// confirmation lives in the client (the UI enables the button only after
// showing the preview); this route is the act itself.
func (s *Server) handleFrictionSend(w http.ResponseWriter, r *http.Request) {
	store := frictionStore()
	rep, err := store.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	cfg, err := s.frictionConfig(r)
	if err != nil {
		writeError(w, err)
		return
	}
	title, body := friction.Render(*rep, friction.Host{OS: runtime.GOOS})
	pub := friction.GitHubDiscussions{Repo: cfg.Friction.Repo, Category: cfg.Friction.Category}
	url, err := pub.Publish(r.Context(), title, body)
	if err != nil {
		writeError(w, err)
		return
	}
	sent, err := store.MarkSent(r.Context(), rep.ID, url)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sent)
}

// handleFrictionDrop implements DELETE /v1/friction/{id}: delete the local
// file. Nothing remote is touched, so no confirmation is asked for.
func (s *Server) handleFrictionDrop(w http.ResponseWriter, r *http.Request) {
	if err := frictionStore().Drop(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

// frictionConfig resolves the engine-level config for the request's
// workspace (defaults plus the user file plus that workspace's file), which
// is where friction.repo and friction.category live.
func (s *Server) frictionConfig(r *http.Request) (config.Config, error) {
	return config.Load(engineFrom(r.Context()).Workspace())
}
