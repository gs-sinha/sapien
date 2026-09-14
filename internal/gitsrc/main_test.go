package gitsrc

import (
	"os"
	"testing"
)

// TestMain gives every git command these tests run, including the ones
// CommitPaths runs on the product's behalf, a fixed identity. Without it the
// commit tests pass only on a machine whose git can find or guess a user: a
// CI runner has none configured and `git commit` exits 128.
func TestMain(m *testing.M) {
	setTestGitIdentity()
	os.Exit(m.Run())
}

func setTestGitIdentity() {
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME":     "Sapien Test",
		"GIT_AUTHOR_EMAIL":    "test@sapien.dev",
		"GIT_COMMITTER_NAME":  "Sapien Test",
		"GIT_COMMITTER_EMAIL": "test@sapien.dev",
	} {
		os.Setenv(k, v)
	}
}
