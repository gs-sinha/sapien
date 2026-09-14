package workspace

import (
	"os"
	"path/filepath"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// LocalDir returns the absolute path of ws's local tier, <ws>/local: the
// flows and memories that belong to this machine only (PLAN §7b). Unlike
// flows/ and memories/, it is not created by Init; EnsureLocalDir creates
// it on the first local write, so a workspace that never uses the tier
// never carries the directory.
func LocalDir(ws *domain.Workspace) string {
	return filepath.Join(ws.Dir, domain.LocalDir)
}

// LocalFlowsDir returns <ws>/local/flows, the local-tier flows directory.
func LocalFlowsDir(ws *domain.Workspace) string {
	return filepath.Join(LocalDir(ws), domain.FlowsDir)
}

// LocalMemoriesDir returns <ws>/local/memories, where flow-scoped memories
// of a local-tier flow live so the tier is self-contained: promoting the
// flow moves them, and nothing about a local flow ever reaches the team's
// memories/ by accident.
func LocalMemoriesDir(ws *domain.Workspace) string {
	return filepath.Join(LocalDir(ws), domain.MemoriesDir)
}

// localExamplesDirName mirrors example.ExamplesDir ("examples"); spelled
// out here rather than imported so this package does not need to depend on
// internal/example just for one directory name.
const localExamplesDirName = "examples"

// LocalExamplesDir returns <ws>/local/examples, the local tier's examples
// directory (PLAN §7b): workspace-scope examples default here, same as a
// new memory or flow, until they are moved to the workspace tier.
func LocalExamplesDir(ws *domain.Workspace) string {
	return filepath.Join(LocalDir(ws), localExamplesDirName)
}

// EnsureLocalDir creates the local tier -- local/, local/flows/,
// local/memories/, local/examples/ -- and makes it self-ignoring with a
// local/.gitignore containing "*", exactly as Init does for .sapien/. The
// workspace is usually a git repo shared with the team, and the whole
// point of the tier is that its contents never ride a commit, so the
// ignore file is written in the same step as the directory rather than
// left to the user. Idempotent: an existing .gitignore is never rewritten,
// so a developer who deliberately edited it keeps their version.
func EnsureLocalDir(ws *domain.Workspace) error {
	for _, dir := range []string{LocalDir(ws), LocalFlowsDir(ws), LocalMemoriesDir(ws), LocalExamplesDir(ws)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return errs.Wrap(errs.Internal, err, "creating %s", dir)
		}
	}
	ignore := filepath.Join(LocalDir(ws), ".gitignore")
	if _, err := os.Stat(ignore); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return errs.Wrap(errs.Internal, err, "checking %s", ignore)
	}
	if err := os.WriteFile(ignore, []byte("*\n"), 0o644); err != nil {
		return errs.Wrap(errs.Internal, err, "writing %s", ignore)
	}
	return nil
}
