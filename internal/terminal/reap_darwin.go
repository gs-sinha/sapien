//go:build darwin

package terminal

import "golang.org/x/sys/unix"

// listProcs reads the process table with the kern.proc sysctl.
//
// Darwin's kinfo_proc reports the parent pid and the process group id
// directly, but not a usable session: its e_sess field is the kernel
// address of the session struct, and current macOS zeroes it out rather
// than leak kernel addresses (verified on 15.x -- every row comes back
// 0x0). The session id therefore comes from a getsid(2) call per pid,
// which the kernel answers for any process, not just our own. That is a
// few hundred cheap syscalls on a busy desktop, well under a millisecond
// in total, and it is the only way to know which processes a pane owns. A
// pid that exits between the sysctl and its getsid reports no session and
// is left alone.
func listProcs() ([]procEntry, bool) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, false
	}
	out := make([]procEntry, 0, len(kps))
	for i := range kps {
		pid := int(kps[i].Proc.P_pid)
		if pid <= 0 {
			continue
		}
		sid, err := unix.Getsid(pid)
		if err != nil {
			sid = 0
		}
		out = append(out, procEntry{
			pid:  pid,
			ppid: int(kps[i].Eproc.Ppid),
			pgid: int(kps[i].Eproc.Pgid),
			sid:  sid,
		})
	}
	return out, true
}
