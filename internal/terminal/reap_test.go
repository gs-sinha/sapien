package terminal

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pids in these tables are made up; what matters is the shape.
// daemon is this process (the one that started the pane), pane is the
// process pty.Start put in a session of its own.
const (
	fakeDaemon = 100
	fakePane   = 200
)

func TestPaneKillSetTakesSessionMembersThatLeftThePaneProcessGroup(t *testing.T) {
	procs := []procEntry{
		{pid: 1, ppid: 0, pgid: 1, sid: 1},
		{pid: fakeDaemon, ppid: 1, pgid: fakeDaemon, sid: fakeDaemon},
		{pid: fakePane, ppid: fakeDaemon, pgid: fakePane, sid: fakePane},
		{pid: 300, ppid: fakePane, pgid: 300, sid: fakePane}, // background job, own group
		{pid: 301, ppid: 300, pgid: 300, sid: fakePane},      // its child
		{pid: 400, ppid: fakePane, pgid: fakePane, sid: fakePane},
		{pid: 500, ppid: 1, pgid: 500, sid: 500}, // someone else entirely
	}

	set := paneKillSetFrom(procs, fakePane, fakeDaemon)

	assert.Equal(t, []int{300, 301, 400}, set.pids)
	// Not the pane's own group: Close kills that one itself.
	assert.Equal(t, []int{300}, set.groups)
}

func TestPaneKillSetTakesASessionMemberAlreadyReparentedToInit(t *testing.T) {
	// The job's own shell exited first, so the parent link to the pane is
	// already gone: only the session says who it belongs to.
	procs := []procEntry{
		{pid: 1, ppid: 0, pgid: 1, sid: 1},
		{pid: fakeDaemon, ppid: 1, pgid: fakeDaemon, sid: fakeDaemon},
		{pid: fakePane, ppid: fakeDaemon, pgid: fakePane, sid: fakePane},
		{pid: 300, ppid: 1, pgid: 250, sid: fakePane},
	}

	set := paneKillSetFrom(procs, fakePane, fakeDaemon)

	assert.Equal(t, []int{300}, set.pids)
	assert.Equal(t, []int{250}, set.groups)
}

func TestPaneKillSetTakesADescendantThatLeftThePaneSession(t *testing.T) {
	// A descendant that called setsid() is in no session of ours, so the
	// parent walk is the only thing that can account for it -- which is
	// why the walk has to run before the pane dies and the link is lost.
	procs := []procEntry{
		{pid: 1, ppid: 0, pgid: 1, sid: 1},
		{pid: fakeDaemon, ppid: 1, pgid: fakeDaemon, sid: fakeDaemon},
		{pid: fakePane, ppid: fakeDaemon, pgid: fakePane, sid: fakePane},
		{pid: 300, ppid: fakePane, pgid: 300, sid: 300}, // setsid'd away
		{pid: 301, ppid: 300, pgid: 300, sid: 300},
	}

	set := paneKillSetFrom(procs, fakePane, fakeDaemon)

	assert.Equal(t, []int{300, 301}, set.pids)
	assert.Equal(t, []int{300}, set.groups)
}

func TestPaneKillSetNeverTakesInitTheDaemonOrThePaneItself(t *testing.T) {
	// Nothing in a real process table looks like this; the point is that
	// even if the table claimed init and the daemon were in the pane's
	// session, they would not be signalled.
	procs := []procEntry{
		{pid: 1, ppid: 0, pgid: 1, sid: fakePane},
		{pid: fakeDaemon, ppid: 1, pgid: fakeDaemon, sid: fakePane},
		{pid: fakePane, ppid: fakeDaemon, pgid: fakePane, sid: fakePane},
	}

	set := paneKillSetFrom(procs, fakePane, fakeDaemon)

	assert.Empty(t, set.pids)
	assert.Empty(t, set.groups)
}

func TestPaneKillSetIsEmptyWhenThePanePidIsNoLongerOurChild(t *testing.T) {
	// The pane exited and was reaped, and something unrelated has since
	// been given its pid. Its children are not ours to kill.
	procs := []procEntry{
		{pid: 1, ppid: 0, pgid: 1, sid: 1},
		{pid: fakeDaemon, ppid: 1, pgid: fakeDaemon, sid: fakeDaemon},
		{pid: fakePane, ppid: 999, pgid: fakePane, sid: fakePane},
		{pid: 300, ppid: fakePane, pgid: 300, sid: fakePane},
	}

	set := paneKillSetFrom(procs, fakePane, fakeDaemon)

	assert.Empty(t, set.pids)
	assert.Empty(t, set.groups)
}

func TestPaneKillSetIsEmptyWhenThePaneIsNotInTheProcessTableAtAll(t *testing.T) {
	procs := []procEntry{
		{pid: 1, ppid: 0, pgid: 1, sid: 1},
		{pid: fakeDaemon, ppid: 1, pgid: fakeDaemon, sid: fakeDaemon},
	}

	set := paneKillSetFrom(procs, fakePane, fakeDaemon)

	assert.Empty(t, set.pids)
	assert.Empty(t, set.groups)
}

func TestPaneKillSetSkipsTheSessionSweepWhenThePaneIsNotASessionLeader(t *testing.T) {
	// pty.Start always calls setsid, so this cannot happen -- but if it
	// ever did, the pane would share the daemon's session, and sweeping
	// that session would kill the daemon and everything around it. Only
	// the processes the parent walk positively reaches may be killed, and
	// no process groups at all, since group membership proves nothing
	// once the session boundary is gone.
	procs := []procEntry{
		{pid: 1, ppid: 0, pgid: 1, sid: 1},
		{pid: fakeDaemon, ppid: 1, pgid: fakeDaemon, sid: fakeDaemon},
		{pid: fakePane, ppid: fakeDaemon, pgid: fakePane, sid: fakeDaemon},
		{pid: 300, ppid: fakePane, pgid: fakeDaemon, sid: fakeDaemon},
		{pid: 900, ppid: 1, pgid: 900, sid: fakeDaemon}, // the user's own shell, say
	}

	set := paneKillSetFrom(procs, fakePane, fakeDaemon)

	assert.Equal(t, []int{300}, set.pids)
	assert.Empty(t, set.groups)
}

func TestPaneKillSetTerminatesOnACyclicProcessTable(t *testing.T) {
	// The kernel cannot produce a cycle, but the table is read one row at
	// a time while processes come and go, so the walk must not depend on
	// it being a tree.
	procs := []procEntry{
		{pid: 1, ppid: 0, pgid: 1, sid: 1},
		{pid: fakeDaemon, ppid: 1, pgid: fakeDaemon, sid: fakeDaemon},
		{pid: fakePane, ppid: fakeDaemon, pgid: fakePane, sid: fakePane},
		{pid: 300, ppid: fakePane, pgid: 300, sid: 300},
		{pid: 301, ppid: 300, pgid: 300, sid: 300},
		{pid: 302, ppid: 301, pgid: 300, sid: 300},
		{pid: 303, ppid: 303, pgid: 303, sid: 303}, // its own parent
	}
	// 300 is reachable twice, the second time through 302.
	procs = append(procs, procEntry{pid: 300, ppid: 302, pgid: 300, sid: 300})

	done := make(chan killSet, 1)
	go func() { done <- paneKillSetFrom(procs, fakePane, fakeDaemon) }()
	select {
	case set := <-done:
		assert.Equal(t, []int{300, 301, 302}, set.pids)
	case <-time.After(5 * time.Second):
		t.Fatal("paneKillSetFrom did not terminate on a cyclic process table")
	}
}

// This is the assumption everything else rests on: that the platform's
// process table reader agrees with what pty.Start actually did.
func TestListProcsReportsThePaneAsOurChildAndItsOwnSessionLeader(t *testing.T) {
	m := NewManager()
	sess, err := m.Start(context.Background(), Spec{Command: "/bin/sh", Args: []string{"-c", "cat"}})
	require.NoError(t, err)
	t.Cleanup(func() { sess.Close() })

	procs, ok := listProcs()
	if !ok {
		t.Skipf("no process table reader for %s; Close falls back to the group kill", runtime.GOOS)
	}

	pane, found := findProcEntry(procs, sess.cmd.Process.Pid)
	require.True(t, found, "the pane is missing from the process table")
	assert.Equal(t, os.Getpid(), pane.ppid, "the pane should be a direct child of this process")
	assert.Equal(t, pane.pid, pane.sid, "pty.Start should have made the pane a session leader")
	assert.Equal(t, pane.pid, pane.pgid, "pty.Start should have made the pane a process group leader")

	self, found := findProcEntry(procs, os.Getpid())
	require.True(t, found, "this process is missing from the process table")
	assert.NotEqual(t, pane.sid, self.sid, "the sweep would take this process with it")
}

func findProcEntry(procs []procEntry, pid int) (procEntry, bool) {
	for _, p := range procs {
		if p.pid == pid {
			return p, true
		}
	}
	return procEntry{}, false
}
