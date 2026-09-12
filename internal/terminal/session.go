package terminal

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Session is one running PTY-attached process, returned by Manager.Start.
type Session struct {
	cmd  *exec.Cmd
	ptmx *os.File

	// output carries chunks read from the PTY (stdout and stderr are not
	// distinguishable once merged through a PTY). readLoop closes it once
	// reading ends, whether because the process exited or Close aborted it.
	output chan []byte
	// abort is closed by Close to unblock a readLoop goroutine that's
	// blocked trying to send into output (e.g. because nothing is
	// currently draining it), so Close can never leak that goroutine.
	abort chan struct{}

	closeOnce sync.Once

	// waitDone closes once cmd.Wait has returned, recording its result and
	// the process's exit code.
	waitDone chan struct{}
	waitMu   sync.Mutex
	waitErr  error
	exitCode int

	idleMu    sync.Mutex
	idleTimer *time.Timer
	idleDur   time.Duration

	manager *Manager
}

// Output returns the channel of data read from the PTY, in the order
// received. It closes once the process's output ends: read it with `for
// chunk := range sess.Output()` to drain until then.
func (s *Session) Output() <-chan []byte { return s.output }

// Write sends p to the PTY as if typed at the keyboard (the process's
// stdin) and resets the idle timer.
func (s *Session) Write(p []byte) (int, error) {
	s.touchIdle()
	return s.ptmx.Write(p)
}

// Resize changes the PTY's terminal size. Cols and Rows of zero or less
// are ignored (a no-op, not an error), so a caller need not filter out a
// malformed resize message itself.
func (s *Session) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return pty.Setsize(s.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// Close kills the terminal process, everything it started (SIGKILL), and
// releases the PTY. It is idempotent and safe to call concurrently with
// the process exiting on its own (via Wait/waitLoop) or with the idle
// timeout firing.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.stopIdle()
		close(s.abort)
		if s.cmd.Process != nil {
			pid := s.cmd.Process.Pid
			// Take the census of the pane's descendants first: once the
			// pane process dies its children are reparented to pid 1 and
			// the parent links that identify them are gone. Skip it
			// entirely once waitLoop has reaped the process, because then
			// the pid is no longer ours and may already have been
			// recycled by something unrelated -- see reap.go, which is
			// suspicious about this too.
			var escaped killSet
			select {
			case <-s.waitDone:
			default:
				escaped = paneKillSet(pid)
			}
			// pty.Start creates a separate session/process group. Kill the
			// group so agent subprocesses do not survive a disconnected tab.
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = s.cmd.Process.Kill()
			// That reaches everything `claude` and `codex` spawn, but not
			// what a shell pane with job control on put in a process
			// group of its own; the census above is what covers those.
			escaped.kill()
		}
		_ = s.ptmx.Close()
	})
	return nil
}

// Wait blocks until the process has exited -- including one killed by
// Close or the idle timeout -- and returns cmd.Wait's error (non-nil for
// a non-zero exit, exactly as os/exec documents). Call ExitCode afterward
// for the numeric status to report to a client.
func (s *Session) Wait() error {
	<-s.waitDone
	s.waitMu.Lock()
	defer s.waitMu.Unlock()
	return s.waitErr
}

// Done returns a channel closed once the process has exited, for a caller
// that wants to select on it rather than block in Wait.
func (s *Session) Done() <-chan struct{} { return s.waitDone }

// ExitCode returns the process's exit status. It reads 0 until Wait
// returns (or Done closes); -1 if the process was killed by a signal
// rather than exiting normally.
func (s *Session) ExitCode() int {
	s.waitMu.Lock()
	defer s.waitMu.Unlock()
	return s.exitCode
}

// readLoop copies the PTY's output into s.output until reading fails
// (normally because the process exited and the kernel tore down the PTY,
// or because Close closed it directly), then closes s.output.
func (s *Session) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			s.touchIdle()
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			select {
			case s.output <- chunk:
			case <-s.abort:
				close(s.output)
				return
			}
		}
		if err != nil {
			close(s.output)
			return
		}
	}
}

// waitLoop reaps the process, records its result, and unregisters the
// session from its Manager.
func (s *Session) waitLoop() {
	err := s.cmd.Wait()
	code := 0
	switch {
	case s.cmd.ProcessState != nil:
		code = s.cmd.ProcessState.ExitCode() // -1 if killed by a signal
	case err != nil:
		code = -1
	}

	s.waitMu.Lock()
	s.exitCode = code
	s.waitErr = err
	s.waitMu.Unlock()

	close(s.waitDone)
	s.stopIdle()
	s.manager.remove(s)
	_ = s.ptmx.Close() // no-op if Close already closed it
}

// watchContext kills the session if ctx is cancelled before the process
// exits on its own.
func (s *Session) watchContext(ctx context.Context) {
	select {
	case <-ctx.Done():
		s.Close()
	case <-s.waitDone:
	}
}

func (s *Session) armIdle(d time.Duration) {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	s.idleDur = d
	s.idleTimer = time.AfterFunc(d, func() { s.Close() })
}

// touchIdle resets the idle countdown; called on every read from and
// write to the PTY so a session with any recent I/O is never killed for
// inactivity.
func (s *Session) touchIdle() {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	if s.idleTimer != nil {
		s.idleTimer.Reset(s.idleDur)
	}
}

func (s *Session) stopIdle() {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
}
