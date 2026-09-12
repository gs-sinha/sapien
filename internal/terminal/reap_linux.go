//go:build linux

package terminal

import (
	"os"
	"strconv"
	"strings"
)

// listProcs reads the process table out of /proc. One small file per
// process and no syscalls beyond the reads: /proc/<pid>/stat carries the
// parent pid, the process group and the session as its fourth, fifth and
// sixth fields. A directory that is not a number is not a process, and a
// stat file that has vanished by the time we read it belonged to a
// process that has already exited, so both are skipped rather than
// treated as failures -- returning false is reserved for "this platform
// cannot answer at all", which downgrades Close to a plain group kill.
func listProcs() ([]procEntry, bool) {
	dir, err := os.Open("/proc")
	if err != nil {
		return nil, false
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, false
	}
	out := make([]procEntry, 0, len(names))
	for _, name := range names {
		pid, err := strconv.Atoi(name)
		if err != nil || pid <= 0 {
			continue
		}
		buf, err := os.ReadFile("/proc/" + name + "/stat")
		if err != nil {
			continue
		}
		if e, ok := parseProcStat(pid, string(buf)); ok {
			out = append(out, e)
		}
	}
	return out, true
}

// parseProcStat pulls ppid, pgid and sid out of one /proc/<pid>/stat
// line. The second field is the executable name in parentheses, unquoted
// and unescaped, so it can contain both spaces and parentheses ("(sleep
// 30) (x)" is a legal comm); splitting the whole line on spaces therefore
// misnumbers every field after it. Everything numeric we want comes after
// the *last* ')', which is unambiguous.
func parseProcStat(pid int, stat string) (procEntry, bool) {
	end := strings.LastIndex(stat, ")")
	if end < 0 {
		return procEntry{}, false
	}
	// After the comm: state, ppid, pgrp, session, ...
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 4 {
		return procEntry{}, false
	}
	ppid, err1 := strconv.Atoi(fields[1])
	pgid, err2 := strconv.Atoi(fields[2])
	sid, err3 := strconv.Atoi(fields[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return procEntry{}, false
	}
	return procEntry{pid: pid, ppid: ppid, pgid: pgid, sid: sid}, true
}
