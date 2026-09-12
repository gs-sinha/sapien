package terminal

// Killing a pane means killing everything the pane started, and that is
// harder than `kill(-pid)` makes it look.
//
// pty.Start puts the pane process in a session and a process group of its
// own, so signalling that group reaches every process that stayed in it.
// For a `claude` or `codex` pane that is the whole tree: neither uses job
// control, so their children inherit the pane's process group. An
// interactive shell pane is the exception, and it is the common case for
// the shell tab: with job control on, zsh and bash put every job in a
// *separate* process group, which `kill(-panePid)` cannot reach. Those
// jobs survive the tab being closed and end up reparented to pid 1, still
// running, invisible to the user who closed the tab.
//
// The session, not the process group, is the boundary that holds. A
// process inherits its session across fork and exec and can leave it only
// by calling setsid() itself, so "in the pane's session" is the most
// precise statement of "started by this pane" the kernel will give us: far
// safer than matching on a command name or a working directory, and wider
// than the process group. So Close snapshots the process table and takes
// every process whose session id is the pane's, plus anything reachable
// from the pane by parent->child links -- the second pass catches a
// descendant that did call setsid(), but only while the pane is still
// alive to anchor the chain, which is why the census has to happen before
// any killing.
//
// Reading the process table is the platform-specific part: reap_darwin.go
// uses the kern.proc sysctl, reap_linux.go reads /proc, and reap_other.go
// declines, which leaves Close doing exactly what it did before -- the
// group kill and nothing else. (The package is already Unix-only, since
// syscall.Kill and creack/pty are; "other" means the remaining BSDs, not
// Windows.)

import (
	"os"
	"sort"
	"syscall"
)

// procEntry is one row of the process table: the fields every supported
// platform can report cheaply.
type procEntry struct {
	pid, ppid, pgid int
	// sid is the process's session id, or 0 if the platform could not
	// determine it (typically because the process exited while the table
	// was being read). Zero is never matched against, so a row we cannot
	// place is a row we leave alone.
	sid int
}

// killSet is what Close signals on top of the pane's own process group:
// the pane's descendants that escaped that group, and the distinct groups
// they belong to.
type killSet struct {
	pids   []int
	groups []int
}

// paneKillSet takes a census of the processes belonging to the pane
// running as panePid. It reads the process table and takes no locks, so
// it cannot deadlock against a waitLoop or an idle timer racing Close for
// the same session, and it does not block on the processes it is about to
// kill.
func paneKillSet(panePid int) killSet {
	procs, ok := listProcs()
	if !ok {
		return killSet{}
	}
	return paneKillSetFrom(procs, panePid, os.Getpid())
}

// paneKillSetFrom is the decision, split out from reading the table so it
// can be tested against a process table we made up rather than the
// machine's.
//
// It is deliberately suspicious. Getting this wrong means killing the
// user's own processes, so a pid is a candidate only if the snapshot
// positively places it inside the pane: the row for panePid has to still
// be one of our own children (a pid we have already reaped could have
// been recycled by an unrelated process, and then its "descendants" would
// be someone else's), and the session sweep runs only if that row is a
// session leader, which is the shape pty.Start's Setsid guarantees. If
// either check fails we return nothing and leave the existing group kill
// as the only action.
func paneKillSetFrom(procs []procEntry, panePid, selfPid int) killSet {
	byPid := make(map[int]procEntry, len(procs))
	children := make(map[int][]int, len(procs))
	for _, p := range procs {
		byPid[p.pid] = p
		if p.ppid != p.pid { // a self-parent would make the walk below loop
			children[p.ppid] = append(children[p.ppid], p.pid)
		}
	}

	pane, found := byPid[panePid]
	if !found || panePid <= 1 || pane.ppid != selfPid {
		return killSet{}
	}

	doomed := make(map[int]bool, 8)

	// Everything the kernel still counts as part of the pane's session.
	// pane.sid == panePid says the pane really is the session leader
	// pty.Start made it; without that we could be reading a pane that
	// somehow shares our own session, and sweeping that session would
	// kill the daemon and whatever started it.
	sessionAnchored := pane.sid == panePid
	if sessionAnchored {
		for _, p := range procs {
			if p.sid == panePid {
				doomed[p.pid] = true
			}
		}
	}

	// Plus everything reachable from the pane by parent->child links,
	// which is how a descendant that called setsid() -- and so left the
	// session swept above -- is still accounted for.
	queue := []int{panePid}
	walked := map[int]bool{panePid: true}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, kid := range children[parent] {
			if walked[kid] || kid <= 1 || kid == selfPid {
				continue
			}
			walked[kid] = true
			doomed[kid] = true
			queue = append(queue, kid)
		}
	}

	// The pane itself, this process, and init are never ours to signal
	// here: Close kills the pane (and its group) directly.
	delete(doomed, panePid)
	delete(doomed, selfPid)

	selfPgid := byPid[selfPid].pgid
	var set killSet
	seenGroup := map[int]bool{}
	for pid := range doomed {
		if pid <= 1 {
			continue
		}
		set.pids = append(set.pids, pid)
		// A process group never spans two sessions -- setpgid refuses to
		// move a process into a group outside its own -- so the group of
		// a process we have placed inside the pane's session lies inside
		// that session too. Killing the group as well as the pid costs
		// nothing and catches anything forked into it since the snapshot.
		// Only worth it once the session is anchored: without that the
		// group membership proves nothing, and never our own group.
		g := byPid[pid].pgid
		if !sessionAnchored || g <= 1 || g == panePid || g == selfPgid || seenGroup[g] {
			continue
		}
		seenGroup[g] = true
		set.groups = append(set.groups, g)
	}
	// Map iteration order is random; sorting keeps the kills (and the
	// tests) deterministic.
	sort.Ints(set.pids)
	sort.Ints(set.groups)
	return set
}

// kill SIGKILLs everything in the set, groups first so that a process
// forked since the census dies with its parent's group rather than
// outliving it.
func (k killSet) kill() {
	for _, pgid := range k.groups {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	for _, pid := range k.pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
