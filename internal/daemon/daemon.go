// Package daemon manages daemon.json (PLAN §4, §22): the file a running
// "sapien serve" writes so that one-shot CLI invocations, "sapien mcp", and
// the desktop app can find it, verify it's still alive and version-matched,
// and talk to its HTTP API (internal/server) instead of spinning up their
// own in-process engine.Local.
package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// Info is the persisted contents of daemon.json.
type Info struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	Token     string    `json:"token"`
	Version   string    `json:"version"`
	Started   time.Time `json:"started"`
	Workspace string    `json:"workspace"`
}

// Path returns <ws.Dir>/.sapien/daemon.json.
func Path(ws *domain.Workspace) string {
	return filepath.Join(ws.Dir, domain.WorkspaceStateDir, "daemon.json")
}

// Write persists info to Path(ws) with mode 0600, via a temp file in the
// same directory followed by an atomic rename, so a concurrent Read never
// observes a partially written file.
func Write(ws *domain.Workspace, info *Info) error {
	path := Path(ws)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errs.Wrap(errs.Internal, err, "creating %s", dir)
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return errs.Wrap(errs.Internal, err, "encoding daemon info")
	}

	tmp, err := os.CreateTemp(dir, ".daemon.json.*.tmp")
	if err != nil {
		return errs.Wrap(errs.Internal, err, "creating temp file in %s", dir)
	}
	tmpName := tmp.Name()
	// Cleaned up on any early return; a no-op once the rename below succeeds.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errs.Wrap(errs.Internal, err, "writing %s", tmpName)
	}
	if err := tmp.Close(); err != nil {
		return errs.Wrap(errs.Internal, err, "closing %s", tmpName)
	}
	if err := os.Chmod(tmpName, 0600); err != nil {
		return errs.Wrap(errs.Internal, err, "chmod %s", tmpName)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return errs.Wrap(errs.Internal, err, "renaming %s to %s", tmpName, path)
	}
	return nil
}

// Read loads Path(ws). A missing file is reported as errs.DaemonUnavailable
// so callers (Find, in particular) can distinguish "no daemon" from a
// genuine I/O failure.
func Read(ws *domain.Workspace) (*Info, error) {
	path := Path(ws)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errs.Wrap(errs.DaemonUnavailable, err, "no daemon info at %s", path)
		}
		return nil, errs.Wrap(errs.Internal, err, "reading %s", path)
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, errs.Wrap(errs.Internal, err, "parsing %s", path)
	}
	return &info, nil
}

// Remove deletes Path(ws). Removing an already-absent file is not an error.
func Remove(ws *domain.Workspace) error {
	path := Path(ws)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return errs.Wrap(errs.Internal, err, "removing %s", path)
	}
	return nil
}

// NewToken returns a fresh bearer token: 32 random bytes, hex-encoded.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read only fails if the platform's entropy source is
		// broken, which no caller can meaningfully recover from.
		panic("daemon: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// processAlive reports whether pid names a process that still exists, via
// a signal-0 kill (POSIX's standard existence probe: it delivers no signal
// but still fails with ESRCH if the process is gone).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// Running reports whether info names a process that still exists. It says
// nothing about whether that process is answering requests, and that is the
// point: it is the deliberately weaker half of Alive. A daemon that is
// swapped out, part-way through a long index, or stopped (SIGSTOP) fails
// Alive's two-second health probe while its process -- along with its file
// watcher, its open database and its several hundred MB of resident memory
// -- is very much still there.
//
// Ask Alive before deciding whether to *talk* to a daemon; ask Running
// before deciding whether to *signal* one, or whether to forget it. Treating
// an unresponsive daemon as absent is precisely how orphans were made: its
// daemon.json was removed, nothing could name that pid again, and it kept
// indexing the workspace until the machine was rebooted.
func Running(info *Info) bool {
	return info != nil && processAlive(info.PID)
}

// Alive reports whether info's process still exists AND its HTTP API
// answers GET /v1/health with 200 within two seconds. Both checks matter: a PID
// can be reused by an unrelated process once the daemon exits, and a
// process can exist but be hung, mid-shutdown, or listening on a port that
// no longer matches (e.g. after a crash-and-restart raced this check).
func Alive(ctx context.Context, info *Info) bool {
	if info == nil || !processAlive(info.PID) {
		return false
	}

	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/v1/health", info.Port), nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// Consume the small health body so frequent CLI probes can reuse their
	// connection instead of opening a new one for every discovery.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == http.StatusOK
}

// Find looks up the daemon for ws (PLAN §4's "one-shot CLI" rule).
//
//   - No daemon.json, or one naming a process that has exited: the
//     stale file (if any) is removed and Find returns (nil, nil) so the
//     caller falls back to engine.Local.
//   - A process that exists but fails its health check: return
//     errs.DaemonUnavailable and preserve discovery, preventing a busy daemon
//     from being replaced or a second local engine from being opened.
//   - A live daemon whose Version doesn't match version: Find returns
//     errs.Conflict with Details{"daemon_version": info.Version}, so the
//     caller can report the mismatch and offer "sapien serve --restart".
//   - A live, version-matching daemon: Find returns its Info.
func Find(ctx context.Context, ws *domain.Workspace, version string) (*Info, error) {
	info, err := Read(ws)
	if err != nil {
		if errs.Is(err, errs.DaemonUnavailable) {
			return nil, nil
		}
		return nil, err
	}

	if !Alive(ctx, info) {
		if processAlive(info.PID) {
			return nil, errs.New(errs.DaemonUnavailable, "daemon pid %d is running but not responding; retry shortly", info.PID)
		}
		_ = Remove(ws)
		return nil, nil
	}

	if info.Version != version {
		return nil, errs.New(errs.Conflict, "daemon for workspace %q is running version %q, not %q", ws.Dir, info.Version, version).
			WithDetail("daemon_version", info.Version)
	}

	return info, nil
}
