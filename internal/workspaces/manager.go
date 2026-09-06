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
		open:    map[string]*entry{},
		primary: primary.Dir,
		opts:    opts,
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
// Opening acquires the workspace's daemon.lock: a workspace already being
// served by another live daemon is refused with errs.Conflict rather than
// opened a second time.
func (m *Manager) Engine(dir string) (engine.Engine, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

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
	// Opening a workspace is also how it becomes offerable later: a
	// directory reached by --workspace once should show up in the picker
	// without a separate "add" step. A registry write failure is never
	// fatal to serving it.
	_ = config.AddWorkspace(ws.Dir)
	return eng, nil
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
