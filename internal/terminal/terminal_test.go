package terminal

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/errs"
)

// readUntil drains ch until its accumulated text contains want, or fails
// the test after a generous timeout.
func readUntil(t *testing.T, ch <-chan []byte, want string) string {
	t.Helper()
	var buf strings.Builder
	deadline := time.After(5 * time.Second)
	for {
		select {
		case chunk, ok := <-ch:
			if !ok {
				t.Fatalf("output closed before %q appeared; got %q", want, buf.String())
			}
			buf.Write(chunk)
			if strings.Contains(buf.String(), want) {
				return buf.String()
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q in output; got %q", want, buf.String())
		}
	}
}

func TestStartRunsCommandAndStreamsOutput(t *testing.T) {
	m := NewManager()
	sess, err := m.Start(context.Background(), Spec{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo hi; cat"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { sess.Close() })

	readUntil(t, sess.Output(), "hi")
}

func TestWriteIsEchoedBackThroughCat(t *testing.T) {
	m := NewManager()
	sess, err := m.Start(context.Background(), Spec{
		Command: "/bin/sh",
		Args:    []string{"-c", "cat"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { sess.Close() })

	_, err = sess.Write([]byte("hello-world\n"))
	require.NoError(t, err)

	readUntil(t, sess.Output(), "hello-world")
}

func TestResize(t *testing.T) {
	m := NewManager()
	sess, err := m.Start(context.Background(), Spec{Command: "/bin/sh", Args: []string{"-c", "cat"}})
	require.NoError(t, err)
	t.Cleanup(func() { sess.Close() })

	assert.NoError(t, sess.Resize(120, 40))
	// Non-positive sizes are ignored, not an error.
	assert.NoError(t, sess.Resize(0, 0))
	assert.NoError(t, sess.Resize(-1, 24))
}

func TestCloseKillsProcessAndWaitReturns(t *testing.T) {
	m := NewManager()
	sess, err := m.Start(context.Background(), Spec{Command: "/bin/sh", Args: []string{"-c", "cat"}})
	require.NoError(t, err)

	require.NoError(t, sess.Close())

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit after Close")
	}
	_ = sess.Wait() // killed: Wait's error is non-nil, that's expected
}

func TestExitCodeForNaturalExit(t *testing.T) {
	m := NewManager()
	sess, err := m.Start(context.Background(), Spec{Command: "/bin/sh", Args: []string{"-c", "exit 3"}})
	require.NoError(t, err)

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit")
	}
	_ = sess.Wait()
	assert.Equal(t, 3, sess.ExitCode())
}

func TestMaxSessionsCap(t *testing.T) {
	m := NewManager()
	m.MaxSessions = 2

	var sessions []*Session
	for i := 0; i < 2; i++ {
		s, err := m.Start(context.Background(), Spec{Command: "/bin/sh", Args: []string{"-c", "cat"}})
		require.NoError(t, err)
		sessions = append(sessions, s)
	}
	t.Cleanup(func() {
		for _, s := range sessions {
			s.Close()
		}
	})

	_, err := m.Start(context.Background(), Spec{Command: "/bin/sh", Args: []string{"-c", "cat"}})
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	assert.Equal(t, 2, m.Count())
}

func TestIdleTimeoutKillsAnInactiveSession(t *testing.T) {
	m := NewManager()
	m.IdleTimeout = 50 * time.Millisecond
	sess, err := m.Start(context.Background(), Spec{Command: "/bin/sh", Args: []string{"-c", "cat"}})
	require.NoError(t, err)

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("idle timeout did not close the session")
	}
}

func TestIdleTimeoutResetsOnActivity(t *testing.T) {
	m := NewManager()
	m.IdleTimeout = 150 * time.Millisecond
	sess, err := m.Start(context.Background(), Spec{Command: "/bin/sh", Args: []string{"-c", "cat"}})
	require.NoError(t, err)
	t.Cleanup(func() { sess.Close() })

	// Keep writing (I/O) faster than the idle timeout for longer than the
	// timeout would otherwise allow; the session must survive.
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, err := sess.Write([]byte("x"))
		require.NoError(t, err)
		select {
		case <-sess.Done():
			t.Fatal("session closed despite ongoing activity")
		case <-time.After(30 * time.Millisecond):
		}
	}
}

func TestContextCancelClosesSession(t *testing.T) {
	m := NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	sess, err := m.Start(ctx, Spec{Command: "/bin/sh", Args: []string{"-c", "cat"}})
	require.NoError(t, err)

	cancel()

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling ctx did not close the session")
	}
}

func TestSessionRemovedFromManagerOnExit(t *testing.T) {
	m := NewManager()
	sess, err := m.Start(context.Background(), Spec{Command: "/bin/sh", Args: []string{"-c", "exit 0"}})
	require.NoError(t, err)

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit")
	}
	// waitLoop's manager.remove(s) races the Done() close only in that both
	// happen in waitLoop before returning; give it a moment to finish.
	require.Eventually(t, func() bool { return m.Count() == 0 }, time.Second, 10*time.Millisecond)
}

func TestCloseAll(t *testing.T) {
	m := NewManager()
	for i := 0; i < 2; i++ {
		_, err := m.Start(context.Background(), Spec{Command: "/bin/sh", Args: []string{"-c", "cat"}})
		require.NoError(t, err)
	}
	m.CloseAll()
	require.Eventually(t, func() bool { return m.Count() == 0 }, time.Second, 10*time.Millisecond)
}

func TestConcurrentStartsRespectLimit(t *testing.T) {
	var m Manager
	m.MaxSessions = 1
	defer m.CloseAll()
	results := make(chan error, 12)
	for i := 0; i < cap(results); i++ {
		go func() {
			_, err := m.Start(context.Background(), Spec{Command: "/bin/sh", Args: []string{"-c", "sleep 30"}})
			results <- err
		}()
	}
	successes := 0
	for i := 0; i < cap(results); i++ {
		if err := <-results; err == nil {
			successes++
		} else {
			assert.Equal(t, errs.Conflict, errs.CodeOf(err))
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, m.Count())
}

// The child ignores terminal hangup, as an agent's background tools may do.
func TestCloseKillsProcessGroup(t *testing.T) {
	m := NewManager()
	marker := filepath.Join(t.TempDir(), "survived")
	sess, err := m.Start(context.Background(), Spec{
		Command: "/bin/sh",
		Args:    []string{"-c", `trap '' HUP; (sleep 0.3; echo leaked > "$MARKER") & echo ready; wait`},
		Env:     append(os.Environ(), "MARKER="+marker),
	})
	require.NoError(t, err)
	defer m.CloseAll()
	readUntil(t, sess.Output(), "ready")
	require.NoError(t, sess.Close())
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("terminal did not exit")
	}
	time.Sleep(500 * time.Millisecond)
	require.NoFileExists(t, marker, "terminal child survived Close")
}

// escapedSleepMarker is how long the escaped-process-group test's
// children sleep: long enough that they never exit on their own, and
// distinctive enough that a stray `sleep 987654` on the machine is
// unmistakably one this test leaked.
const escapedSleepMarker = "987654"

// The regression this package's session sweep exists for: a shell pane
// with job control on puts every background job in a process group of its
// own, which kill(-panePid) cannot reach. Before Close swept the pane's
// session those jobs outlived the browser tab, reparented to pid 1.
func TestCloseKillsChildrenInEscapedProcessGroups(t *testing.T) {
	pidsPath := filepath.Join(t.TempDir(), "pids")
	// `set -m` turns job control on in a non-interactive shell, which is
	// what puts each `&` job in a process group of its own -- the same
	// thing the user's interactive zsh or bash does in a real pane. The
	// nested sh adds a grandchild in a third group, one level further out
	// than any group kill can see. "nested" is written last, so the test
	// can tell a complete pid file from a half-written one.
	script := `set -m
sleep ` + escapedSleepMarker + ` &
echo "$!" >> "$PIDS"
sh -c 'sleep ` + escapedSleepMarker + ` & echo "$!" >> "$PIDS"; echo nested >> "$PIDS"; wait' &
echo "$!" >> "$PIDS"
echo ready
wait`

	m := NewManager()
	sess, err := m.Start(context.Background(), Spec{
		Command: "/bin/sh",
		Args:    []string{"-c", script},
		Env:     append(os.Environ(), "PIDS="+pidsPath),
	})
	require.NoError(t, err)
	defer m.CloseAll()
	readUntil(t, sess.Output(), "ready")

	require.Eventually(t, func() bool {
		data, err := os.ReadFile(pidsPath)
		return err == nil && strings.Contains(string(data), "nested")
	}, 5*time.Second, 20*time.Millisecond, "pane script never finished recording its children")
	data, err := os.ReadFile(pidsPath)
	require.NoError(t, err)
	var kids []int
	for _, field := range strings.Fields(string(data)) {
		if field == "nested" {
			continue
		}
		pid, err := strconv.Atoi(field)
		require.NoError(t, err, "unexpected line %q in the pid file", field)
		kids = append(kids, pid)
	}
	require.Len(t, kids, 3, "expected two background jobs and one grandchild")
	// Kill anything still standing however this test ends, so a failure
	// never leaves marker processes on the machine.
	t.Cleanup(func() {
		for _, pid := range kids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	panePgid, err := syscall.Getpgid(sess.cmd.Process.Pid)
	require.NoError(t, err)
	escaped := 0
	for _, pid := range kids {
		pgid, err := syscall.Getpgid(pid)
		require.NoError(t, err, "child %d should still be running before Close", pid)
		if pgid != panePgid {
			escaped++
		}
	}
	require.NotZero(t, escaped, "this shell kept every job in the pane's process group, so the test is no longer reproducing the bug")

	require.NoError(t, sess.Close())

	for _, pid := range kids {
		require.Eventually(t, func() bool { return syscall.Kill(pid, 0) != nil },
			5*time.Second, 20*time.Millisecond, "child %d survived Close", pid)
	}
}

func TestCloseIsIdempotentUnderConcurrentCalls(t *testing.T) {
	m := NewManager()
	sess, err := m.Start(context.Background(), Spec{Command: "/bin/sh", Args: []string{"-c", "cat"}})
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, sess.Close())
		}()
	}
	wg.Wait()

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit after Close")
	}
	// And again once waitLoop has reaped the process: Close must stay a
	// no-op rather than go hunting for the descendants of a pid that is
	// no longer ours.
	assert.NoError(t, sess.Close())
}
