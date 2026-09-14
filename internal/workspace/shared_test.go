package workspace_test

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// hermeticGitEnv and runGitCmd mirror the pattern used throughout the git
// test suites elsewhere in the repo (e.g. internal/gitsrc/testutil_test.go):
// a throwaway HOME plus a forced-off global git config, so these tests
// never touch the developer's real git configuration.
func hermeticGitEnv(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	return []string{
		"HOME=" + home,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	}
}

func runGitCmd(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "git %s: %s", strings.Join(args, " "), stderr.String())
}

func TestIsShared_TrackedFileWithRemote(t *testing.T) {
	env := hermeticGitEnv(t)
	dir := t.TempDir()
	ws, err := workspace.Init(dir, "shared")
	require.NoError(t, err)

	runGitCmd(t, dir, env, "init", "-b", "main")
	runGitCmd(t, dir, env, "add", domain.WorkspaceFileName)
	runGitCmd(t, dir, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", "init")
	runGitCmd(t, dir, env, "remote", "add", "origin", "git@example.com:acme/demo.git")

	assert.True(t, workspace.IsShared(ws))
}

func TestIsShared_UntrackedFileInsideRepoWithRemote(t *testing.T) {
	// This is the README quickstart's own shape: `sapien init demo` run
	// inside a git repository (the sapien repo itself, in practice) that
	// has a remote. demo/sapien.workspace.yaml is never committed there,
	// so it must not be treated as shared.
	env := hermeticGitEnv(t)
	dir := t.TempDir()
	ws, err := workspace.Init(dir, "shared")
	require.NoError(t, err)

	runGitCmd(t, dir, env, "init", "-b", "main")
	runGitCmd(t, dir, env, "remote", "add", "origin", "git@example.com:acme/demo.git")
	// sapien.workspace.yaml is left untracked.

	assert.False(t, workspace.IsShared(ws),
		"a workspace file that was never committed is not shared, even inside a repo with a remote")
}

func TestIsShared_TrackedFileButNoRemote(t *testing.T) {
	env := hermeticGitEnv(t)
	dir := t.TempDir()
	ws, err := workspace.Init(dir, "shared")
	require.NoError(t, err)

	runGitCmd(t, dir, env, "init", "-b", "main")
	runGitCmd(t, dir, env, "add", domain.WorkspaceFileName)
	runGitCmd(t, dir, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", "init")

	assert.False(t, workspace.IsShared(ws), "no remote means nobody else can actually pull this workspace")
}

func TestIsShared_NotARepository(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.Init(dir, "shared")
	require.NoError(t, err)

	assert.False(t, workspace.IsShared(ws))
}

func TestIsShared_NilOrEmptyDir(t *testing.T) {
	assert.False(t, workspace.IsShared(nil))
	assert.False(t, workspace.IsShared(&domain.Workspace{}))
}
