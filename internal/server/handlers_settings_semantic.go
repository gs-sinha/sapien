package server

import (
	"context"
	"net/http"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// handleSettingsSemanticGet implements GET /v1/settings/semantic (PLAN
// §34f item 5): the effective, merged semantic-search configuration (never
// the api_key) plus live status.
func (s *Server) handleSettingsSemanticGet(w http.ResponseWriter, r *http.Request) {
	out, err := engineFrom(r.Context()).Settings().GetSemantic(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSettingsSemanticPut implements PUT /v1/settings/semantic: writes
// and applies the config for req.Scope ("user" by default) to this
// request's workspace, then -- for a "user"-scope change on a daemon
// serving several workspaces -- reapplies each OTHER open workspace's own
// merged config too (a "user"-scope write just changed the default a
// workspace without its own override resolves to; one with an override is
// unaffected since reapplying its own file's merge is a no-op).
func (s *Server) handleSettingsSemanticPut(w http.ResponseWriter, r *http.Request) {
	var req engine.SemanticPutRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	eng := engineFrom(r.Context())
	out, err := eng.Settings().PutSemantic(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	if req.Scope == "" || req.Scope == "user" {
		s.applySemanticToOtherWorkspaces(r.Context(), eng)
	}
	writeJSON(w, http.StatusOK, out)
}

// semanticReloader is implemented by *local.Local (settings_semantic.go's
// ReloadSemantic); not part of engine.SettingsAPI, since engine.Remote has
// no equivalent -- a daemon's own open workspaces are always engine.Local.
type semanticReloader interface {
	ReloadSemantic(ctx context.Context) error
}

// applySemanticToOtherWorkspaces reapplies every other open workspace's own
// merged semantic config. Best effort: one workspace failing to reload
// never fails the request that already changed the user-level file
// successfully. A no-op without a Manager (a single-workspace server).
func (s *Server) applySemanticToOtherWorkspaces(ctx context.Context, changed engine.Engine) {
	if s.workspaces == nil {
		return
	}
	for dir, eng := range s.workspaces.Engines() {
		if eng == changed {
			continue
		}
		reloader, ok := eng.(semanticReloader)
		if !ok {
			continue
		}
		if err := reloader.ReloadSemantic(ctx); err != nil {
			s.logger.Warn("reapplying semantic settings to workspace failed", "workspace", dir, "error", err)
		}
	}
}

// handleSettingsSemanticTest implements POST /v1/settings/semantic/test:
// tries a posted config without saving it. Always HTTP 200; ok:false
// carries the provider's (or the request's own) rejection.
func (s *Server) handleSettingsSemanticTest(w http.ResponseWriter, r *http.Request) {
	var req domain.SemanticProbe
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	out, err := engineFrom(r.Context()).Settings().TestSemantic(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSettingsSemanticReindex implements POST /v1/settings/semantic/
// reindex: starts a full rebuild in the background and returns 202;
// progress is reported through semantic.index events.
func (s *Server) handleSettingsSemanticReindex(w http.ResponseWriter, r *http.Request) {
	if err := engineFrom(r.Context()).Settings().ReindexSemantic(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, nil)
}

// handleSettingsOllamaStatus implements GET /v1/settings/semantic/ollama:
// probes an Ollama endpoint's /api/tags. Always HTTP 200;
// reachable:false carries why.
func (s *Server) handleSettingsOllamaStatus(w http.ResponseWriter, r *http.Request) {
	baseURL := r.URL.Query().Get("base_url")
	out, err := engineFrom(r.Context()).Settings().OllamaStatus(r.Context(), baseURL)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSettingsOllamaPull implements POST /v1/settings/semantic/ollama/
// pull: starts an `ollama pull` in the background and returns 202;
// progress is reported through semantic.pull events. 409 when that model
// is already being pulled.
func (s *Server) handleSettingsOllamaPull(w http.ResponseWriter, r *http.Request) {
	var req domain.OllamaPullRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := engineFrom(r.Context()).Settings().OllamaPull(r.Context(), req); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, nil)
}
