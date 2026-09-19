package server

import (
	"context"
	"net/http"
	"time"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/selfupdate"
)

// updateInfoResponse is GET /v1/update's body, also what POST
// /v1/update/check and PUT /v1/settings/updates answer with once they have
// changed the state it reports (PLAN §34f item 4).
type updateInfoResponse struct {
	Current        string    `json:"current"`
	Latest         string    `json:"latest,omitempty"`
	Available      bool      `json:"available"`
	CheckedAt      time.Time `json:"checked_at,omitempty"`
	ReleaseURL     string    `json:"release_url,omitempty"`
	InstallMethod  string    `json:"install_method"`
	CanSelfUpgrade bool      `json:"can_self_upgrade"`
	Command        string    `json:"command"`
	CheckEnabled   bool      `json:"check_enabled"`
	Error          string    `json:"error,omitempty"`
}

// updateInfo assembles updateInfoResponse from the on-disk cache (never a
// network call itself -- see handleUpdateCheck for the one that is) plus
// the statically-derived install method and the live check_enabled state
// from the primary workspace's config. It uses s.engine.Workspace() (the
// primary), not the per-request resolved one: update checking is a
// daemon-wide concept, exactly like GET /v1/daemon, not a per-workspace
// one, and PUT /v1/settings/updates always writes the user-level config
// file regardless of which workspace a request names.
func (s *Server) updateInfo(ctx context.Context) (updateInfoResponse, error) {
	cache, err := selfupdate.LoadCacheFile(selfupdate.CachePath())
	if err != nil {
		return updateInfoResponse{}, err
	}

	exe, err := selfupdate.Executable()
	if err != nil {
		return updateInfoResponse{}, err
	}
	method := selfupdate.DetectMethod(s.version, exe)

	cfg, err := config.Load(s.engine.Workspace())
	if err != nil {
		return updateInfoResponse{}, err
	}

	out := updateInfoResponse{
		Current:        s.version,
		InstallMethod:  string(method),
		CanSelfUpgrade: selfupdate.CanSelfUpgrade(method),
		Command:        selfupdate.Command(method),
		CheckEnabled:   cfg.Updates.Enabled(),
	}
	if cache != nil {
		out.Latest = cache.Latest
		out.CheckedAt = cache.CheckedAt
		out.Error = cache.Error
		if cache.Latest != "" {
			out.Available = selfupdate.IsNewer(s.version, cache.Latest)
			out.ReleaseURL = selfupdate.ReleaseURL(cache.Latest)
		}
	}
	return out, nil
}

// handleUpdateGet implements GET /v1/update: a fast, network-free read of
// the last check's result (see handleUpdateCheck for the one that hits the
// network).
func (s *Server) handleUpdateGet(w http.ResponseWriter, r *http.Request) {
	info, err := s.updateInfo(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleUpdateCheck implements POST /v1/update/check: the Settings page's
// "check now" button. Unlike the daemon's own background check
// (serve.go's kickBackgroundUpdateCheck), this always hits the network --
// bypassing selfupdate.Due's 24h throttle on purpose, since checking on
// request is the entire point of a manual button -- but a failed check is
// still answered with the (updated) cache rather than an error response:
// the request succeeded at asking, even if the network didn't cooperate,
// and the resulting Error field is exactly how the UI learns that.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if _, err := selfupdate.Refresh(r.Context(), selfupdate.RefreshOptions{BaseURL: s.updateBaseURL}); err != nil {
		writeError(w, err)
		return
	}
	info, err := s.updateInfo(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// updateApplyRequest is POST /v1/update/apply's body.
type updateApplyRequest struct {
	Version string `json:"version,omitempty"`
}

// handleUpdateApply implements POST /v1/update/apply: refuses with 409 for
// any install method that isn't "script" (Homebrew, go install, and dev
// builds are managed by something else, and self-replacing their binary
// would only fight it), otherwise downloads, verifies, and installs the
// release in-process via selfupdate.Apply, and -- once that has succeeded
// and the 202 response has been flushed -- calls RestartHook exactly like
// POST /v1/daemon/restart, so the newly-installed binary actually starts
// running.
func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	var req updateApplyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}

	exe, err := selfupdate.Executable()
	if err != nil {
		writeError(w, err)
		return
	}
	method := selfupdate.DetectMethod(s.version, exe)
	if !selfupdate.CanSelfUpgrade(method) {
		writeError(w, errs.New(errs.Conflict, "sapien was installed via %s; it cannot upgrade itself", method).
			WithDetail("install_method", string(method)).
			WithDetail("command", selfupdate.Command(method)))
		return
	}

	result, err := selfupdate.Apply(r.Context(), selfupdate.ApplyOptions{Version: req.Version, BaseURL: s.updateBaseURL})
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"applying": true, "version": result.Version})
	flushResponse(w)
	if s.restartHook != nil {
		go s.restartHook()
	}
}

// updateSettingsRequest is PUT /v1/settings/updates's body.
type updateSettingsRequest struct {
	Check bool `json:"check"`
}

// handleUpdateSettingsSet implements PUT /v1/settings/updates: writes
// `updates: check:` to the user config file (config.SetUpdatesCheck,
// preserving every other key and comment) and answers with the resulting
// updateInfoResponse, so the Settings page can update its toggle from the
// response instead of issuing a second GET.
func (s *Server) handleUpdateSettingsSet(w http.ResponseWriter, r *http.Request) {
	var req updateSettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := config.SetUpdatesCheck(req.Check); err != nil {
		writeError(w, err)
		return
	}
	info, err := s.updateInfo(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}
