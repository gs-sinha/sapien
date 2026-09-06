package gitsrc

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// hermeticGitEnv returns the extra environment used by every git invocation
// in these tests (both the Manager under test, via Options.Env, and the
// test fixture helpers below, via runGit's env parameter), so nothing here
// ever depends on -- or mutates -- the developer's real git configuration,
// and nothing ever touches the network.
func hermeticGitEnv(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	return []string{
		"HOME=" + home,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	}
}

// runGit runs git with args in dir (the test process's own cwd when dir is
// ""), using env in addition to PATH, failing the test immediately on
// error.
func runGit(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s (dir=%q): %v\n%s", strings.Join(args, " "), dir, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// newBareRepo creates a local bare repository (no commits yet, HEAD
// symbolically pointing at refs/heads/main) and returns its filesystem path
// and its file:// URL.
func newBareRepo(t *testing.T, env []string) (dir, url string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "repo.git")
	runGit(t, "", env, "init", "--bare", "-b", "main", dir)
	return dir, "file://" + dir
}

// commitAndPush clones bareDir into a fresh working directory, writes
// files (path relative to the repo root -> content, creating parent
// directories as needed), commits them with a fixed test author/committer
// identity, and pushes the result to whichever branch is currently checked
// out (bareDir's default branch on the first call). It returns the new
// commit's sha. Calling it repeatedly builds a linear history on the bare
// repo.
func commitAndPush(t *testing.T, bareDir string, env []string, files map[string]string, msg string) string {
	t.Helper()
	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)

	for rel, content := range files {
		p := filepath.Join(work, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}

	runGit(t, work, env, "add", "-A")
	runGit(t, work, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", msg)
	runGit(t, work, env, "push", "origin", "HEAD")
	return runGit(t, work, env, "rev-parse", "HEAD")
}

// createBranch creates a new branch in bareDir starting from fromRef
// (a ref that already exists on origin, e.g. "main"), optionally adding a
// commit on top when files is non-empty, and pushes it. It returns the
// resulting branch tip's commit sha.
func createBranch(t *testing.T, bareDir string, env []string, branch, fromRef string, files map[string]string, msg string) string {
	t.Helper()
	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	runGit(t, work, env, "checkout", "-b", branch, "origin/"+fromRef)

	if len(files) > 0 {
		for rel, content := range files {
			p := filepath.Join(work, rel)
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		}
		runGit(t, work, env, "add", "-A")
		runGit(t, work, env, "-c", "user.name=Sapien Test", "-c", "user.email=test@sapien.dev", "commit", "-m", msg)
	}

	runGit(t, work, env, "push", "origin", branch)
	return runGit(t, work, env, "rev-parse", "HEAD")
}

// createTag tags fromRef (an existing ref on origin, e.g. "main") as tag and
// pushes it.
func createTag(t *testing.T, bareDir string, env []string, tag, fromRef string) string {
	t.Helper()
	work := t.TempDir()
	runGit(t, "", env, "clone", bareDir, work)
	runGit(t, work, env, "checkout", "origin/"+fromRef)
	sha := runGit(t, work, env, "rev-parse", "HEAD")
	runGit(t, work, env, "tag", tag)
	runGit(t, work, env, "push", "origin", tag)
	return sha
}

// testManager builds a Manager rooted at a fresh temp cache dir, using env
// for every git invocation it makes.
func testManager(t *testing.T, env []string) *Manager {
	t.Helper()
	return New(Options{
		CacheDir: filepath.Join(t.TempDir(), "repos"),
		Timeout:  10 * time.Second,
		Env:      env,
	})
}
