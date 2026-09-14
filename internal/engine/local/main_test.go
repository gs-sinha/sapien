package local

import (
	"os"
	"testing"
)

// TestMain gives every git command these tests run, including the commits
// and pushes the engine makes for flow, memory and example tiers, a fixed
// identity. Without it those tests pass only on a machine whose git can find
// or guess a user: a CI runner has none configured and `git commit` exits 128.
func TestMain(m *testing.M) {
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME":     "Sapien Test",
		"GIT_AUTHOR_EMAIL":    "test@sapien.dev",
		"GIT_COMMITTER_NAME":  "Sapien Test",
		"GIT_COMMITTER_EMAIL": "test@sapien.dev",
	} {
		os.Setenv(k, v)
	}
	os.Exit(m.Run())
}
