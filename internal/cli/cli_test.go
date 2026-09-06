package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/cli"
)

// run executes cli.Execute with args and returns (stdout, stderr, exit code).
func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Execute(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func TestVersion_Human(t *testing.T) {
	stdout, stderr, code := run(t, "version")
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "sapien")
}

func TestVersion_JSON(t *testing.T) {
	stdout, stderr, code := run(t, "version", "--json")
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)

	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Contains(t, got, "version")
	assert.Contains(t, got, "commit")
	assert.Contains(t, got, "date")
}

func TestInit_Human(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := run(t, "init", dir, "--name", "logistics")
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "logistics")
	assert.FileExists(t, filepath.Join(dir, "sapien.workspace.yaml"))
}

func TestInit_JSON(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := run(t, "init", dir, "--name", "logistics", "--json")
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, dir, got["dir"])
	assert.Equal(t, "logistics", got["name"])
}

func TestInit_ConflictExitCode(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "init", dir, "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_CONFLICT", got["code"])
}

func TestEnvList_JSON(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", dir, "env", "list", "--json")
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)

	var envs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &envs))
	require.Len(t, envs, 1)
	assert.Equal(t, "local", envs[0]["name"])
}

func TestEnvList_Human(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)

	stdout, _, code := run(t, "--workspace", dir, "env", "list")
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "NAME")
	assert.Contains(t, stdout, "local")
}

func TestEnvShow(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", dir, "env", "show", "local", "--json")
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)

	var env map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &env))
	assert.Equal(t, "local", env["name"])
}

func TestEnvShow_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", dir, "env", "show", "staging", "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_ENV_NOT_FOUND", got["code"])
}

func TestEnvUse(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", dir, "env", "use", "local", "--json")
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)

	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "local", got["default_environment"])

	data, err := os.ReadFile(filepath.Join(dir, "sapien.workspace.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "default_environment: local")
}

func TestEnvUse_UnknownEnv(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)

	_, stderr, code := run(t, "--workspace", dir, "env", "use", "staging", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_ENV_NOT_FOUND", got["code"])
}

func TestMissingWorkspace_JSON(t *testing.T) {
	dir := t.TempDir() // empty, no workspace anywhere above it in the temp tree
	stdout, stderr, code := run(t, "--workspace", dir, "env", "list", "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_WORKSPACE_NOT_FOUND", got["code"])
	assert.NotEmpty(t, got["hint"])
}

func TestMissingWorkspace_Human(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := run(t, "--workspace", dir, "env", "list")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "E_WORKSPACE_NOT_FOUND")
	assert.Contains(t, stderr, "hint:")
}
