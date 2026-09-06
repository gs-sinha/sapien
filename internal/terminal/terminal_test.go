package terminal

import (
	"context"
	"strings"
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
