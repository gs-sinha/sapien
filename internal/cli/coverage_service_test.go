package cli_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine/enginetest"
)

// --- service add: git-URL detection builds a domain.Source{Kind: git, ...},
// recorded verbatim by the Fake's Add. ---

func TestServiceAdd_GitURL_SSHShorthand(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "service", "add", "git@github.com:acme/widgets.git",
		"--name", "widgets", "--ref", "main", "--subdir", "api", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	src := got["source"].(map[string]any)
	assert.Equal(t, "git", src["type"])
	assert.Equal(t, "git@github.com:acme/widgets.git", src["url"])
	assert.Equal(t, "main", src["ref"])
	assert.Equal(t, "api", src["subdir"])

	call := lastCall(fake, "Services.Add")
	args := call.Args.(map[string]any)
	src2 := args["source"].(domain.Source)
	assert.Equal(t, domain.SourceGit, src2.Kind)
	assert.Equal(t, "main", src2.Ref)
	assert.Equal(t, "api", src2.Subdir)
}

func TestServiceAdd_GitURL_SSHScheme(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "add", "ssh://git@github.com/acme/widgets.git", "--name", "w2", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	call := lastCall(fake, "Services.Add")
	src := call.Args.(map[string]any)["source"].(domain.Source)
	assert.Equal(t, domain.SourceGit, src.Kind)
}

func TestServiceAdd_GitURL_HTTPS(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "add", "https://github.com/acme/widgets.git", "--name", "w3", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	call := lastCall(fake, "Services.Add")
	src := call.Args.(map[string]any)["source"].(domain.Source)
	assert.Equal(t, domain.SourceGit, src.Kind)
}

func TestServiceAdd_HTTPSWithoutGitSuffix_IsGit(t *testing.T) {
	// No ".git" suffix: looksLikeGitURL's https:// branch requires it, so
	// this falls through to the "local path" branch even though it's a URL.
	dir, fake := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "add", "https://example.com/not-git", "--name", "w4", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	call := lastCall(fake, "Services.Add")
	src := call.Args.(map[string]any)["source"].(domain.Source)
	assert.Equal(t, domain.SourceGit, src.Kind)
	assert.Equal(t, "https://example.com/not-git", src.URL)
}

func TestServiceAdd_LocalPath(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "add", "./my-service", "--name", "w5", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	call := lastCall(fake, "Services.Add")
	src := call.Args.(map[string]any)["source"].(domain.Source)
	assert.Equal(t, domain.SourceLocal, src.Kind)
}

func TestServiceAdd_Conflict(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "add", "./order-service", "--name", "order-service", "--json")
	assert.Equal(t, 2, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_CONFLICT", got["code"])

	stdout2, stderr2, code2 := run(t, "--workspace", dir, "service", "add", "./order-service", "--name", "order-service")
	assert.Equal(t, 2, code2)
	assert.Empty(t, stdout2)
	assert.Contains(t, stderr2, "E_CONFLICT")
}

// lastCall returns the most recent recorded Fake call with the given method
// name, failing the test if none was recorded.
func lastCall(fake *enginetest.Fake, method string) enginetest.Call {
	for i := len(fake.Calls) - 1; i >= 0; i-- {
		if fake.Calls[i].Method == method {
			return fake.Calls[i]
		}
	}
	panic("no recorded call for " + method)
}

// --- service list: human table, including both Local and Git SOURCE
// rendering ---

func TestServiceList_Human(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, _, code := run(t, "--workspace", dir, "service", "add", "git@github.com:acme/widgets.git", "--name", "widgets")
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", dir, "service", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "order-service")
	assert.Contains(t, stdout, "services/order-service") // local source path from Seed
	assert.Contains(t, stdout, "widgets")
	assert.Contains(t, stdout, "git@github.com:acme/widgets.git") // git source URL
}

// --- service sync ---

func TestServiceSync_All(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderrOut, code := run(t, "--workspace", dir, "service", "sync", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderrOut)
	var svcs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &svcs))
	assert.NotEmpty(t, svcs)

	stdoutHuman, stderrOut, code := run(t, "--workspace", dir, "service", "sync")
	require.Equal(t, 0, code, "stderr: %s", stderrOut)
	assert.Contains(t, stdoutHuman, "order-service")
}

func TestServiceSync_ByName(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "service", "sync", "order-service", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var svcs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &svcs))
	require.Len(t, svcs, 1)
	assert.Equal(t, "order-service", svcs[0]["name"])
}

func TestServiceSync_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "sync", "no-such-service", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_SERVICE_NOT_FOUND", got["code"])

	_, stderr2, code2 := run(t, "--workspace", dir, "service", "sync", "no-such-service")
	assert.Equal(t, 2, code2)
	assert.Contains(t, stderr2, "E_SERVICE_NOT_FOUND")
}

// --- service remove ---

func TestServiceRemove_Success(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "service", "remove", "order-service")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "order-service")

	stdout2, stderr2, code2 := run(t, "--workspace", dir, "service", "remove", "order-service", "--json")
	assert.Equal(t, 2, code2)
	assert.Empty(t, stdout2)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr2), &got))
	assert.Equal(t, "E_SERVICE_NOT_FOUND", got["code"])
}

func TestServiceRemove_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "service", "remove", "nope", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_SERVICE_NOT_FOUND", got["code"])
}

// --- reindex: human mode, and --json via the Fake (which has no Stats()) ---

func TestReindex_Human(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "reindex")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "order-service")
	assert.Contains(t, stdout, "rider-service")
	assert.Contains(t, stdout, "services:")
}

func TestReindex_JSON_Fake(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "reindex", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Contains(t, got, "services")
	assert.Contains(t, got, "stats")
}
