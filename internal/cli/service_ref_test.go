package cli_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// These tests drive `service set-ref` and `service branches` against the
// enginetest fake (PLAN §34f item 2).

func addGitService(t *testing.T, dir, name string) {
	t.Helper()
	_, stderr, code := run(t, "--workspace", dir, "service", "add",
		"git@github.com:acme/"+name+".git", "--name", name, "--ref", "main")
	require.Equal(t, 0, code, "stderr: %s", stderr)
}

func TestServiceSetRef_Local(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	addGitService(t, dir, "widgets")

	stdout, stderr, code := run(t, "--workspace", dir, "service", "set-ref", "widgets", "release-2")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "set the local ref override")

	call := lastCall(fake, "Services.SetRef")
	args := call.Args.(map[string]any)
	assert.Equal(t, "widgets", args["name"])
	assert.Equal(t, "release-2", args["ref"])
	assert.Equal(t, domain.RefScopeLocal, args["scope"])
}

func TestServiceSetRef_Team(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	addGitService(t, dir, "widgets")

	stdout, stderr, code := run(t, "--workspace", dir, "service", "set-ref", "widgets", "release-2", "--team")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "committed the team ref")

	call := lastCall(fake, "Services.SetRef")
	args := call.Args.(map[string]any)
	assert.Equal(t, domain.RefScopeTeam, args["scope"])
}

func TestServiceSetRef_Clear(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	addGitService(t, dir, "widgets")
	_, _, code := run(t, "--workspace", dir, "service", "set-ref", "widgets", "release-2")
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", dir, "service", "set-ref", "widgets", "--clear")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "cleared the local ref override")
	assert.Equal(t, "Services.ClearRef", lastCall(fake, "Services.ClearRef").Method)
}

func TestServiceSetRef_ClearAndRefTogetherRefused(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	addGitService(t, dir, "widgets")

	_, stderr, code := run(t, "--workspace", dir, "service", "set-ref", "widgets", "release-2", "--clear")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "pass a ref or --clear, not both")
}

func TestServiceSetRef_JSON(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	addGitService(t, dir, "widgets")

	stdout, stderr, code := run(t, "--workspace", dir, "service", "set-ref", "widgets", "release-2", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var svc domain.Service
	require.NoError(t, json.Unmarshal([]byte(stdout), &svc))
	assert.Equal(t, "widgets", svc.Name)
	assert.Equal(t, "release-2", svc.Source.Ref)
}

func TestServiceBranches_Human(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	addGitService(t, dir, "widgets")

	stdout, stderr, code := run(t, "--workspace", dir, "service", "branches", "widgets")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "default: main")
	assert.Contains(t, stdout, "main")
	assert.Equal(t, "Services.Branches", lastCall(fake, "Services.Branches").Method)
}

func TestServiceBranches_JSON(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	addGitService(t, dir, "widgets")

	stdout, stderr, code := run(t, "--workspace", dir, "service", "branches", "widgets", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var out engine.BranchList
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	assert.Equal(t, "main", out.Default)
}
