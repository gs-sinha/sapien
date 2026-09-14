package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubGH answers Publish's two GraphQL calls so `friction send --yes` can
// be driven end to end without GitHub.
const stubGH = `#!/bin/sh
case "$*" in
  *hasDiscussionsEnabled*) echo '{"data":{"repository":{"id":"R_1","hasDiscussionsEnabled":true,"discussionCategories":{"nodes":[{"id":"DIC_1","name":"General","slug":"general"}]}}}}' ;;
  *createDiscussion*) echo '{"data":{"createDiscussion":{"discussion":{"url":"https://github.com/gs-sinha/sapien/discussions/9"}}}}' ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`

func frictionEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SAPIEN_FRICTION_DIR", dir)
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "gh"), []byte(stubGH), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// No workspace: friction must work from anywhere, so point the CLI at
	// a directory that has none and make sure no default workspace leaks in.
	t.Setenv("SAPIEN_WORKSPACE", "")
	return dir
}

// The whole human loop: a report is queued, listed, shown as it would be
// posted, sent through gh, and the queue records where it went.
func TestFriction_AddListShowSendDrop(t *testing.T) {
	dir := frictionEnv(t)

	stdout, stderr, code := run(t, "friction", "add", "get_api omits array examples",
		"--happened", "request_example had no items element",
		"--tried", "build a createShipment body",
		"--would-help", "synthesize one element from the item schema",
		"--tool", "get_api", "--json")
	require.Equal(t, 0, code, stderr)
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Client string `json:"client"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &created))
	assert.True(t, strings.HasPrefix(created.ID, "fr_"), created.ID)
	assert.Equal(t, "pending", created.Status)
	assert.Equal(t, "cli", created.Client)
	assert.FileExists(t, filepath.Join(dir, created.ID+".md"))

	stdout, _, code = run(t, "friction", "list")
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, created.ID)
	assert.Contains(t, stdout, "pending")
	assert.Contains(t, stdout, "get_api omits array examples")

	stdout, _, code = run(t, "friction", "show", created.ID)
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "[agent friction] get_api omits array examples")
	assert.Contains(t, stdout, "request_example had no items element")
	assert.Contains(t, stdout, "synthesize one element")

	stdout, stderr, code = run(t, "friction", "send", created.ID, "--yes")
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, "https://github.com/gs-sinha/sapien/discussions/9")

	raw, err := os.ReadFile(filepath.Join(dir, created.ID+".md"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "status: sent")
	assert.Contains(t, string(raw), "discussions/9")

	_, stderr, code = run(t, "friction", "drop", created.ID)
	require.Equal(t, 0, code, stderr)
	assert.NoFileExists(t, filepath.Join(dir, created.ID+".md"))

	stdout, _, code = run(t, "friction", "list")
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "no friction reports")
}

// Nothing is posted without a human saying so: outside a terminal, send
// needs --yes and otherwise refuses before touching gh.
func TestFriction_SendWithoutConfirmationRefuses(t *testing.T) {
	frictionEnv(t)
	// `go test` may inherit a real terminal on stdin; the refusal under test
	// is the non-interactive one, so stand in a pipe for the duration (a
	// pipe, not /dev/null, which is a character device and so looks like a
	// terminal to isTerminal).
	pr, pw, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, pw.Close())
	defer pr.Close()
	savedStdin := os.Stdin
	os.Stdin = pr
	defer func() { os.Stdin = savedStdin }()

	_, stderr, code := run(t, "friction", "add", "x", "--happened", "y")
	require.Equal(t, 0, code, stderr)

	stdout, _, _ := run(t, "friction", "list", "--json")
	var reports []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &reports))
	require.Len(t, reports, 1)

	_, stderr, code = run(t, "friction", "send", reports[0].ID)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "--yes")

	stdout, _, _ = run(t, "friction", "list")
	assert.Contains(t, stdout, "pending", "the report must still be pending")
}

// A report goes public after review, so a secret is refused when filed.
func TestFriction_AddRefusesSecrets(t *testing.T) {
	frictionEnv(t)
	_, stderr, code := run(t, "friction", "add", "auth header rejected",
		"--happened", "sent ghp_abcdefghijklmnopqrstuvwxyz0123456789ABCD and got 401")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, strings.ToLower(stderr), "secret")
}
