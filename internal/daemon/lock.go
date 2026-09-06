package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

// The workspace lock is the daemon's exclusive claim on a workspace,
// independent of daemon.json. daemon.json is a discovery record that a
// newer daemon overwrites when it replaces an older one; if the older
// process was not actually stopped (the case on 2026-09-06: an orphaned
// pre-upgrade daemon kept watching the same directory and, parsing with an
// older grammar, deleted a flow's index row on every pass), nothing
// referred to it any more. The lock file always names the live holder, so
// serve refuses to start beside one, and replacement stops it.

// LockPath returns <ws.Dir>/.sapien/daemon.lock.
func LockPath(ws *domain.Workspace) string {
	return filepath.Join(ws.Dir, domain.WorkspaceStateDir, "daemon.lock")
}

// Lock is a held workspace lock; Release gives it up.
type Lock struct {
	path string
	pid  int
}

// Acquire claims the workspace for pid. It returns errs.Conflict, with
// Details["pid"] naming the holder, when another live process holds the
// lock; a lock left by a dead process is taken over silently.
func Acquire(ws *domain.Workspace, pid int) (*Lock, error) {
	path := LockPath(ws)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, errs.Wrap(errs.Internal, err, "creating %s", filepath.Dir(path))
	}
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, werr := f.WriteString(strconv.Itoa(pid) + "\n")
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(path)
				return nil, errs.Wrap(errs.Internal, firstErr(werr, cerr), "writing %s", path)
			}
			return &Lock{path: path, pid: pid}, nil
		}
		if !os.IsExist(err) {
			return nil, errs.Wrap(errs.Internal, err, "creating %s", path)
		}
		holder, ok := Holder(ws)
		if ok && holder != pid {
			return nil, errs.New(errs.Conflict, "another daemon (pid %d) holds the workspace lock for %s", holder, ws.Dir).
				WithDetail("pid", holder).
				WithHint("run `sapien serve --restart` to replace it, or `sapien daemon stop`")
		}
		// Stale (dead holder) or our own: take it over.
		_ = os.Remove(path)
	}
	return nil, errs.New(errs.Internal, "could not acquire the workspace lock at %s", path)
}

// Release removes the lock when this process still holds it.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errs.Wrap(errs.Internal, err, "reading %s", l.path)
	}
	if strings.TrimSpace(string(data)) != strconv.Itoa(l.pid) {
		return nil // someone else holds it now; leave it alone
	}
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return errs.Wrap(errs.Internal, err, "removing %s", l.path)
	}
	return nil
}

// Holder reports the pid recorded in the workspace lock when that process
// is alive. ok is false when there is no lock, it is unreadable, or its
// holder has exited.
func Holder(ws *domain.Workspace) (pid int, ok bool) {
	data, err := os.ReadFile(LockPath(ws))
	if err != nil {
		return 0, false
	}
	pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	if !processAlive(pid) {
		return 0, false
	}
	return pid, true
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
