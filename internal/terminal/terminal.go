// Package terminal spawns and manages PTY-backed processes for Sapien's
// agent pane (PLAN §34c Phase 7b): one pseudo-terminal per session, wired
// to a browser WebSocket by internal/server/handlers_terminal.go.
//
// This package is the mechanism, not the policy: it knows nothing about
// HTTP, which commands are safe to run, or which directories a session may
// start in. Deciding that (the allowlist: exactly "claude", "codex", or
// the user's $SHELL, resolved via exec.LookPath, and a directory drawn
// from the workspace/registered services/home) is internal/server's job,
// since that's the package with access to the engine's workspace and
// service list. Manager.Start trusts its Spec completely.
package terminal

import (
	"context"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"

	"github.com/gs-sinha/sapien/internal/errs"
)

// Defaults for Manager.MaxSessions and Manager.IdleTimeout, used when the
// corresponding field is left zero.
const (
	DefaultMaxSessions = 4
	DefaultIdleTimeout = 30 * time.Minute
)

// Spec describes one PTY session to start. Command must already be
// resolved to an executable path (e.g. via exec.LookPath) and Dir must
// already be validated by the caller.
type Spec struct {
	Command string
	Args    []string
	Dir     string
	Env     []string
	// Cols and Rows size the PTY; non-positive values default to 80x24.
	Cols, Rows int
}

// Manager spawns PTY sessions and enforces a cap on how many run at once.
// The zero value is ready to use, applying DefaultMaxSessions and
// DefaultIdleTimeout; set the exported fields before the first Start call
// to override them (tests shrink IdleTimeout to avoid a real 30-minute
// wait). Safe for concurrent use.
type Manager struct {
	MaxSessions int
	IdleTimeout time.Duration

	mu       sync.Mutex
	sessions map[*Session]struct{}
}

// NewManager returns a ready Manager with default limits.
func NewManager() *Manager {
	return &Manager{sessions: map[*Session]struct{}{}}
}

func (m *Manager) maxSessions() int {
	if m.MaxSessions > 0 {
		return m.MaxSessions
	}
	return DefaultMaxSessions
}

func (m *Manager) idleTimeout() time.Duration {
	if m.IdleTimeout > 0 {
		return m.IdleTimeout
	}
	return DefaultIdleTimeout
}

// Count returns the number of sessions Manager is currently tracking.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// Start spawns spec.Command attached to a new PTY, sized Cols x Rows (or
// 80x24 if either is non-positive). It fails with an errs.Conflict
// (E_CONFLICT) error if MaxSessions sessions are already running.
//
// The returned Session is tracked by m until its process exits (however
// that happens: naturally, via Close, or via the idle timeout) or ctx is
// cancelled, whichever comes first; a cancelled ctx kills the process
// exactly as Close does. Callers that want the session to outlive the
// request that started it (as the terminal WebSocket handler does, tying
// process lifetime to the socket rather than to the request context) can
// pass context.Background().
func (m *Manager) Start(ctx context.Context, spec Spec) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions == nil {
		m.sessions = make(map[*Session]struct{})
	}
	if len(m.sessions) >= m.maxSessions() {
		return nil, errs.New(errs.Conflict, "too many concurrent terminal sessions (max %d)", m.maxSessions())
	}

	cols, rows := spec.Cols, spec.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	cmd := exec.Command(spec.Command, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "starting %s", spec.Command)
	}

	sess := &Session{
		cmd:      cmd,
		ptmx:     ptmx,
		output:   make(chan []byte, 64),
		abort:    make(chan struct{}),
		waitDone: make(chan struct{}),
		manager:  m,
	}
	sess.armIdle(m.idleTimeout())

	m.sessions[sess] = struct{}{}

	go sess.readLoop()
	go sess.waitLoop()
	if ctx != nil {
		go sess.watchContext(ctx)
	}

	return sess, nil
}

// remove drops s from the tracked set; called once s's process has
// exited. Deleting an absent key is a no-op, so this is safe to call more
// than once.
func (m *Manager) remove(s *Session) {
	m.mu.Lock()
	delete(m.sessions, s)
	m.mu.Unlock()
}

// CloseAll closes every currently tracked session, killing its process.
// Meant for orderly server shutdown; safe to call with no sessions
// running, and safe (a no-op) on a nil Manager, so a zero-value Server
// that never called NewManager can still call Close unconditionally.
func (m *Manager) CloseAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()
	for _, s := range sessions {
		s.Close()
	}
}
