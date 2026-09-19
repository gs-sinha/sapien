package server

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/selfupdate"
)

// daemonInfoResponse is GET /v1/daemon's body (PLAN §34f item 3).
type daemonInfoResponse struct {
	Version        string    `json:"version"`
	Commit         string    `json:"commit,omitempty"`
	Started        time.Time `json:"started"`
	PID            int       `json:"pid"`
	Port           int       `json:"port"`
	Executable     string    `json:"executable"`
	InstallMethod  string    `json:"install_method"`
	WorkspacesOpen int       `json:"workspaces_open"`
	ActiveRuns     int       `json:"active_runs"`
	Terminals      int       `json:"terminals"`
}

// handleDaemonGet implements GET /v1/daemon: this daemon's identity, how it
// was installed, and a snapshot of its current load, for the Settings
// page's daemon panel.
func (s *Server) handleDaemonGet(w http.ResponseWriter, r *http.Request) {
	exe, err := selfupdate.Executable()
	if err != nil {
		writeError(w, err)
		return
	}
	activeRuns, err := s.activeRunCount(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, daemonInfoResponse{
		Version:        s.version,
		Commit:         s.commit,
		Started:        s.started.UTC(),
		PID:            os.Getpid(),
		Port:           s.port,
		Executable:     exe,
		InstallMethod:  string(selfupdate.DetectMethod(s.version, exe)),
		WorkspacesOpen: len(s.openEngines()),
		ActiveRuns:     activeRuns,
		Terminals:      s.terminal.Count(),
	})
}

// daemonRestartRequest is POST /v1/daemon/restart's body.
type daemonRestartRequest struct {
	Force bool `json:"force,omitempty"`
}

// handleDaemonRestart implements POST /v1/daemon/restart: refuses with 409
// (detail active_runs) when runs are in flight and force wasn't set,
// otherwise answers 202 and -- once that response has been flushed to the
// client -- calls the injected RestartHook, which spawns a detached
// successor daemon. There is deliberately no attempt to wait for the
// successor here: its own `--restart` startup is what stops this process
// (serve.go's refuseIfDaemonAlive), so by the time it would report back,
// this handler's goroutine may already be gone.
func (s *Server) handleDaemonRestart(w http.ResponseWriter, r *http.Request) {
	var req daemonRestartRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}

	if s.restartHook == nil {
		writeError(w, errs.New(errs.NotImplemented, "this daemon was not started in a way that supports restarting itself"))
		return
	}

	if !req.Force {
		n, err := s.activeRunCount(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		if n > 0 {
			writeError(w, errs.New(errs.Conflict, "%d run(s) are in flight", n).
				WithDetail("active_runs", n).
				WithHint("pass force: true to restart anyway"))
			return
		}
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"restarting": true})
	flushResponse(w)
	go s.restartHook()
}

// openEngines returns every engine this daemon currently has open: every
// workspace under s.workspaces when it is set (a multi-workspace daemon,
// PLAN §7b), else just the single Engine a test or an older embedding
// configures directly.
func (s *Server) openEngines() []engine.Engine {
	if s.workspaces != nil {
		open := s.workspaces.Engines()
		out := make([]engine.Engine, 0, len(open))
		for _, eng := range open {
			out = append(out, eng)
		}
		return out
	}
	return []engine.Engine{s.engine}
}

// activeRunCount sums domain.RunRunning runs across every open workspace
// (PLAN §34f item 3's active_runs): both GET /v1/daemon's own field and
// what POST /v1/daemon/restart gates on (409 unless forced). POST
// /v1/update/apply, which also ends in a restart, does not gate on this --
// the contract's 409 there is for "not self-upgradable" only -- so an
// update is never blocked by in-flight work the way a bare restart is; a
// Settings page that cares can still check GET /v1/daemon's active_runs
// itself before offering the button. A run is written with status
// "running" the instant it starts (internal/runner.Run), never lingering
// in "queued", so this one filter is exactly "work that would be
// interrupted".
func (s *Server) activeRunCount(ctx context.Context) (int, error) {
	total := 0
	for _, eng := range s.openEngines() {
		runs, err := eng.Runs().List(ctx, domain.RunFilter{Status: domain.RunRunning, Limit: 1000})
		if err != nil {
			return 0, err
		}
		total += len(runs)
	}
	return total, nil
}
