// Package workspaces holds the engines for every workspace one daemon
// serves (PLAN §7, §21).
//
// Sapien's original shape was one daemon per workspace: `sapien serve`
// opened one engine.Local, and switching workspaces meant a second daemon on
// a second port. That does not survive contact with the browser -- cookies
// are not isolated by port (RFC 6265 §8.5), so two daemons on 127.0.0.1
// overwrite each other's sapien_session -- and it multiplies watchers, git
// timers, and idle timers per workspace.
//
// So one daemon now holds many workspaces. engine.Local was already fully
// self-contained per workspace (its own database handle, catalog, search,
// event bus, watcher, git manager and semantic worker, with no package-level
// state), which is what makes this safe: a Manager is a lazily-populated map
// of directory -> engine, nothing more.
//
// Per-workspace on-disk invariants are unchanged. Each workspace keeps its
// own .sapien state directory, its own database, and its own daemon.lock --
// the Manager acquires that lock when it opens a workspace and releases it
// on Close, so "one process indexes one workspace" still holds, and the
// two-daemon failure of 2026-09-06 stays impossible.
package workspaces

import (
	"sync"
	"time"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/daemon"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/engine/local"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// Options configures a Manager.
type Options struct {
	// Local is the engine.Local option set every workspace is opened with
	// (the daemon passes Watch: true).
	Local local.Options
	// PID is recorded in each workspace's daemon.lock. Defaults to this
	// process's pid.
	PID int
	// Open opens one workspace's engine. Defaults to local.Open; tests
	// substitute a fake so a Manager can be exercised without a database.
	Open func(ws *domain.Workspace, opts local.Options) (engine.Engine, error)
}

// Info describes one workspace a Manager knows about.
type Info struct {
	Dir      string `json:"dir"`
	Name     string `json:"name"`
	Primary  bool   `json:"primary"`
	Open     bool   `json:"open"`
	Services int    `json:"services,omitempty"`
	// Error is set when the workspace is registered but could not be
	// loaded (deleted directory, unparsable workspace file). Such a
	// workspace is still listed -- silently dropping it would leave the
	// user with no way to see why it vanished from the picker.
	Error string `json:"error,omitempty"`
}

type entry struct {
	eng      engine.Engine
	lock     *daemon.Lock
	opened   time.Time
	closeErr error
}

// Manager holds one engine per open workspace, opening lazily on first use.
type Manager struct {
	mu      sync.Mutex
	open    map[string]*entry
	primary string
	opts    Options
	closed  bool
	// registered holds directories Register was called for in this
	// process, so a workspace registered through the API is openable even
	// before (or without) the user config listing it.
	registered map[string]bool
}

// New builds a Manager whose primary workspace is primary (already loaded by
// the caller, since the daemon fails to start if its own workspace does not
// open). The primary's engine is adopted as-is: the caller keeps ownership
// of its lock, and Close never closes it.
func New(primary *domain.Workspace, eng engine.Engine, opts Options) *Manager {
	if opts.Open == nil {
		opts.Open = func(ws *domain.Workspace, o local.Options) (engine.Engine, error) {
			return local.Open(ws, o)
		}
	}
	m := &Manager{
		open:       map[string]*entry{},
		primary:    primary.Dir,
		opts:       opts,
		registered: map[string]bool{},
	}
	// The primary carries no lock here: `sapien serve` acquired it before
	// building the Manager and releases it itself on shutdown.
	m.open[primary.Dir] = &entry{eng: eng, opened: time.Now()}
	return m
}

// Primary returns the workspace directory requests fall back to when they
// name none.
func (m *Manager) Primary() string { return m.primary }

// Engine returns the engine for dir, opening the workspace if this is its
// first use. An empty dir means the primary workspace.
//
// Only the primary, a workspace registered in the user config, or one
// Register was called for in this process is opened this way. Anything
// else is errs.WorkspaceNotFound: a request header is not an invitation.
// This is what makes `sapien workspace forget` mean something -- before,
// a stale browser tab still carrying a forgotten workspace's header
// reopened it on its next request, and "shut the other workspaces down"
// could not be done without stopping the daemon.
//
// Opening acquires the workspace's daemon.lock: a workspace already being
// served by another live daemon is refused with errs.Conflict rather than
// opened a second time.
func (m *Manager) Engine(dir string) (engine.Engine, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.engineLocked(dir, false)
}

// Register opens dir the way Engine does, but allows a directory the user
// config does not list yet, and records it there so it is offerable from
// then on. It is the API's explicit "open this workspace" (POST
// /v1/workspaces), as opposed to the implicit open a request header gets.
func (m *Manager) Register(dir string) (engine.Engine, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	eng, err := m.engineLocked(dir, true)
	if err != nil {
		return nil, err
	}
	if ws := eng.Workspace(); ws != nil {
		// A registry write failure is never fatal to serving it.
		_ = config.AddWorkspace(ws.Dir)
	}
	return eng, nil
}

// engineLocked is Engine's body, called with m.mu held. explicit reports
// a Register call, which may open an unregistered directory.
func (m *Manager) engineLocked(dir string, explicit bool) (engine.Engine, error) {
	if m.closed {
		return nil, errs.New(errs.Internal, "workspace manager is closed")
	}

	if dir == "" {
		dir = m.primary
	}
	ws, err := workspace.Load(workspaceFile(dir))
	if err != nil {
		return nil, err
	}

	if e, ok := m.openEntry(ws.Dir); ok {
		return e.eng, nil
	}

	if !explicit && !workspace.SameDir(ws.Dir, m.primary) && !m.isRegistered(ws.Dir) {
		return nil, errs.New(errs.WorkspaceNotFound, "workspace %s is not registered with this daemon", ws.Dir).
			WithHint("`sapien workspace add " + ws.Dir + "` registers it; a forgotten workspace stays closed until then")
	}

	pid := m.opts.PID
	if pid == 0 {
		pid = selfPID()
	}
	lock, err := daemon.Acquire(ws, pid)
	if err != nil {
		return nil, err
	}

	eng, err := m.opts.Open(ws, m.opts.Local)
	if err != nil {
		_ = lock.Release()
		return nil, err
	}

	m.open[ws.Dir] = &entry{eng: eng, lock: lock, opened: time.Now()}
	if explicit {
		m.registered[ws.Dir] = true
	}
	return eng, nil
}

// isRegistered reports whether dir is offerable: listed in the user config
// or registered through this Manager. Called with m.mu held.
func (m *Manager) isRegistered(dir string) bool {
	for d := range m.registered {
		if workspace.SameDir(d, dir) {
			return true
		}
	}
	known, err := config.KnownWorkspaces()
	if err != nil {
		return false
	}
	for _, d := range known {
		if workspace.SameDir(d, dir) {
			return true
		}
	}
	return false
}

// CloseOne closes dir's engine and releases its lock, so the daemon stops
// watching and indexing it. The primary cannot be closed this way -- it is
// what the daemon was started for; stop the daemon instead. Closing a
// workspace that is not open is not an error. A closed workspace is
// reopened by the next request naming it if it is still registered; pair
// with `sapien workspace forget` to keep it closed.
func (m *Manager) CloseOne(dir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if workspace.SameDir(dir, m.primary) {
		return errs.New(errs.Invalid, "the primary workspace %s cannot be closed while the daemon serves it", m.primary).
			WithHint("`sapien daemon stop` stops the daemon itself")
	}
	delete(m.registered, dir)
	for open, e := range m.open {
		if !workspace.SameDir(open, dir) {
			continue
		}
		var firstErr error
		if err := e.eng.Close(); err != nil {
			firstErr = err
		}
		if e.lock != nil {
			if err := e.lock.Release(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		delete(m.open, open)
		return firstErr
	}
	return nil
}

// openEntry finds an already-open workspace by directory, matching on the
// directory itself rather than the spelling of its path -- see sameDir.
// Called with m.mu held.
func (m *Manager) openEntry(dir string) (*entry, bool) {
	if e, ok := m.open[dir]; ok {
		return e, true
	}
	for other, e := range m.open {
		if workspace.SameDir(dir, other) {
			return e, true
		}
	}
	return nil, false
}

// List reports every workspace this Manager knows about: the ones currently
// open plus every directory registered in the user config, so the picker can
// offer a workspace that has not been opened in this daemon yet.
func (m *Manager) List() []Info {
	m.mu.Lock()
	openDirs := make(map[string]bool, len(m.open))
	for dir := range m.open {
		openDirs[dir] = true
	}
	primary := m.primary
	m.mu.Unlock()

	known, err := config.KnownWorkspaces()
	if err != nil {
		known = nil
	}
	for dir := range openDirs {
		known = append(known, dir)
	}

	out := make([]Info, 0, len(known))
	seen := map[string]bool{}
	for _, dir := range known {
		if seen[dir] || listedAlready(out, dir) {
			continue
		}
		seen[dir] = true

		// Primary/open are decided by directory identity too: the daemon's
		// own primary may be spelled differently from the registry's copy
		// of the same path, and listing it twice -- once "primary", once
		// not -- is what a user sees as a phantom third workspace.
		info := Info{Dir: dir, Primary: workspace.SameDir(dir, primary), Open: isOpen(openDirs, dir)}
		ws, err := workspace.Load(workspaceFile(dir))
		if err != nil {
			info.Error = err.Error()
			info.Name = filepathBase(dir)
		} else {
			info.Name = ws.Name
			info.Services = len(ws.Services)
		}
		out = append(out, info)
	}
	return out
}

// Engines returns every engine this Manager currently has open (the
// primary plus any workspace opened since), for daemon-wide introspection
// that needs to look across all of them -- GET /v1/daemon's active_runs
// (PLAN §34f item 3), summed across every open workspace's in-flight runs.
// The order is unspecified.
func (m *Manager) Engines() []engine.Engine {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]engine.Engine, 0, len(m.open))
	for _, e := range m.open {
		out = append(out, e.eng)
	}
	return out
}

// Close closes every workspace this Manager opened and releases their locks.
// The primary engine, which the Manager did not open, is left to its owner.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	var firstErr error
	for dir, e := range m.open {
		if e.lock == nil {
			continue // the primary: owned by the caller
		}
		if err := e.eng.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := e.lock.Release(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(m.open, dir)
	}
	return firstErr
}

// listedAlready reports whether out already carries an entry for the same
// directory as dir, however it is spelled.
func listedAlready(out []Info, dir string) bool {
	for _, info := range out {
		if workspace.SameDir(info.Dir, dir) {
			return true
		}
	}
	return false
}

// isOpen reports whether any open workspace is the same directory as dir.
func isOpen(openDirs map[string]bool, dir string) bool {
	if openDirs[dir] {
		return true
	}
	for other := range openDirs {
		if workspace.SameDir(dir, other) {
			return true
		}
	}
	return false
}
