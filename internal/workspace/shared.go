package workspace

import (
	"os/exec"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
)

// IsShared reports whether ws's committed workspace file
// (sapien.workspace.yaml) is tracked by a git repository that has at least
// one remote -- the signal `sapien service add <path>` uses to decide
// whether committing a git source (AddFromCheckout) is worth defaulting
// to, because someone besides this machine will actually run this
// workspace.
//
// Being inside *some* git repository is not enough: this project's own
// quickstart runs `sapien init demo` inside the sapien repo itself, and
// the throwaway workspace that creates must keep behaving like the
// untracked, personal playground it is -- adding local paths, exactly as
// today -- until its file is actually committed. So the test is narrower:
// the file itself, not just the directory, has to be tracked.
//
// Shelled out rather than read from .git directly: `ls-files
// --error-unmatch` already knows how to find the enclosing repository from
// any subdirectory of it and how "tracked" is defined (staged-but-not-yet-
// committed counts, a .gitignore rule doesn't); reimplementing that inside
// this package would only get it subtly wrong. Both commands are read-only
// and their own output is discarded; only success/failure and, for
// `remote`, non-emptiness, are used.
func IsShared(ws *domain.Workspace) bool {
	if ws == nil || ws.Dir == "" {
		return false
	}
	if err := exec.Command("git", "-C", ws.Dir, "ls-files", "--error-unmatch", domain.WorkspaceFileName).Run(); err != nil {
		return false
	}
	out, err := exec.Command("git", "-C", ws.Dir, "remote").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}
