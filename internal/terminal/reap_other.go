//go:build !darwin && !linux

package terminal

// listProcs has no implementation here: reading the process table needs
// either the kern.proc sysctl (darwin) or /proc (linux), and there is no
// portable third way. Declining is safe -- paneKillSet then returns an
// empty set and Close falls back to killing the pane and its process
// group, exactly what this package did before the session sweep existed.
// A descendant that left the pane's process group survives on such a
// platform, as it always did.
func listProcs() ([]procEntry, bool) {
	return nil, false
}
