package cli_test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/env"
	"github.com/growsimplee/sapien/internal/workspace"
)

// --- env scaffold ---

func envFilePath(dir, name string) string {
	return filepath.Join(dir, domain.EnvironmentsDir, name+".yaml")
}

func TestEnvScaffold_JSON_UpdatesExistingFile(t *testing.T) {
	// setupFakeEngine's `sapien init` already created environments/local.yaml
	// with no services entries; the Fake it wires in is seeded with two
	// services, each declaring only a "local" service.yaml hint
	// (enginetest.Seed: order-service -> http://localhost:4010,
	// rider-service -> http://localhost:4011).
	dir, _ := setupFakeEngine(t)

	stdout, stderr, code := run(t, "--workspace", dir, "env", "scaffold", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var report env.Report
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	require.True(t, report.Changed())
	require.Len(t, report.Files, 1)

	f := report.Files[0]
	assert.Equal(t, "local", f.Environment)
	assert.False(t, f.Created, "local.yaml already existed")
	require.Len(t, f.Entries, 2)
	assert.Equal(t, "order-service", f.Entries[0].Service)
	assert.Equal(t, "http://localhost:4010", f.Entries[0].BaseURL)
	assert.Equal(t, "rider-service", f.Entries[1].Service)
	assert.Equal(t, "http://localhost:4011", f.Entries[1].BaseURL)

	// Persisted to disk.
	data, err := os.ReadFile(envFilePath(dir, "local"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "http://localhost:4010")
	assert.Contains(t, string(data), "http://localhost:4011")
}

func TestEnvScaffold_Human(t *testing.T) {
	dir, _ := setupFakeEngine(t)

	stdout, stderr, code := run(t, "--workspace", dir, "env", "scaffold")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "updated")
	assert.Contains(t, stdout, envFilePath(dir, "local"))
	assert.Contains(t, stdout, "added order-service.base_url")
	assert.Contains(t, stdout, "added rider-service.base_url")
}

func TestEnvScaffold_NothingNeeded(t *testing.T) {
	dir, _ := setupFakeEngine(t)

	_, stderr, code := run(t, "--workspace", dir, "env", "scaffold")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	// Second run: everything the fake's services declare already has a
	// base_url on record.
	stdout, stderr, code := run(t, "--workspace", dir, "env", "scaffold")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "nothing to scaffold")

	stdout, stderr, code = run(t, "--workspace", dir, "env", "scaffold", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var report env.Report
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	assert.False(t, report.Changed())
	assert.Empty(t, report.Files)
}

func TestEnvScaffold_NoForce_LeavesConflictingBaseURLAlone(t *testing.T) {
	dir, _ := setupFakeEngine(t)

	require.NoError(t, os.WriteFile(envFilePath(dir, "local"),
		[]byte("version: 1\nname: local\nservices:\n  order-service:\n    base_url: http://old.example.com\n"), 0o644))

	stdout, stderr, code := run(t, "--workspace", dir, "env", "scaffold", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var report env.Report
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	require.Len(t, report.Files, 1)
	require.Len(t, report.Files[0].Entries, 1, "order-service's conflicting base_url is untouched; only rider-service is added")
	assert.Equal(t, "rider-service", report.Files[0].Entries[0].Service)

	data, err := os.ReadFile(envFilePath(dir, "local"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "http://old.example.com")
}

func TestEnvScaffold_Force_Overwrites(t *testing.T) {
	dir, _ := setupFakeEngine(t)

	require.NoError(t, os.WriteFile(envFilePath(dir, "local"),
		[]byte("version: 1\nname: local\nservices:\n  order-service:\n    base_url: http://old.example.com\n"), 0o644))

	stdout, stderr, code := run(t, "--workspace", dir, "env", "scaffold", "--force")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "overwrote order-service.base_url")

	data, err := os.ReadFile(envFilePath(dir, "local"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "http://localhost:4010")
	assert.NotContains(t, string(data), "http://old.example.com")
}

// --- env probe ---
//
// Probe reads the environment file straight off disk (like `env
// list`/`show`/`use`, it does not need the engine's service catalog), so
// these tests use a plain `sapien init` workspace and write
// environments/local.yaml directly -- no enginetest.Fake required.

func writeLocalEnv(t *testing.T, dir string, e domain.Environment) {
	t.Helper()
	e.Name = "local"
	if e.Version == 0 {
		e.Version = 1
	}
	ws, err := workspace.Load(filepath.Join(dir, domain.WorkspaceFileName))
	require.NoError(t, err)
	require.NoError(t, workspace.SaveEnvironment(ws, &e))
}

func TestEnvProbe_JSON_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)
	writeLocalEnv(t, dir, domain.Environment{
		Services: map[string]domain.ServiceEnv{"order-service": {BaseURL: srv.URL}},
	})

	stdout, stderr, code := run(t, "--workspace", dir, "env", "probe", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var results []env.ProbeResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &results))
	require.Len(t, results, 1)
	assert.Equal(t, "order-service", results[0].Service)
	assert.Equal(t, 200, results[0].Status)
	assert.Empty(t, results[0].Error)
}

func TestEnvProbe_DefaultEnvironment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)
	writeLocalEnv(t, dir, domain.Environment{
		Services: map[string]domain.ServiceEnv{"order-service": {BaseURL: srv.URL}},
	})

	// No explicit name: falls back to the workspace default ("local").
	stdout, stderr, code := run(t, "--workspace", dir, "env", "probe", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "order-service")
}

func TestEnvProbe_Human_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)
	writeLocalEnv(t, dir, domain.Environment{
		Services: map[string]domain.ServiceEnv{"order-service": {BaseURL: srv.URL}},
	})

	stdout, stderr, code := run(t, "--workspace", dir, "env", "probe")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "SERVICE")
	assert.Contains(t, stdout, "BASE_URL")
	assert.Contains(t, stdout, "RESULT")
	assert.Contains(t, stdout, "LATENCY")
	assert.Contains(t, stdout, "order-service")
	assert.Contains(t, stdout, "200")
}

func TestEnvProbe_Failure_ExitCode1(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)
	writeLocalEnv(t, dir, domain.Environment{
		Services: map[string]domain.ServiceEnv{"order-service": {BaseURL: unreachableProbeURL(t)}},
	})

	stdout, stderr, code := run(t, "--workspace", dir, "env", "probe", "--json")
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, "order-service") // results are still printed
	assert.Contains(t, stderr, "E_ASSERTION_FAILED")
}

func TestEnvProbe_MixedResults_ExitCode1(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()

	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)
	writeLocalEnv(t, dir, domain.Environment{
		Services: map[string]domain.ServiceEnv{
			"order-service": {BaseURL: ok.URL},
			"rider-service": {BaseURL: unreachableProbeURL(t)},
		},
	})

	stdout, _, code := run(t, "--workspace", dir, "env", "probe", "--json")
	assert.Equal(t, 1, code)

	var results []env.ProbeResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &results))
	require.Len(t, results, 2)
}

func TestEnvProbe_NoServicesInEnvironment(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", dir, "env", "probe", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Equal(t, "[]\n", stdout)
}

func TestEnvProbe_UnknownEnvironment(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", dir, "env", "probe", "staging", "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_ENV_NOT_FOUND", got["code"])
}

// unreachableProbeURL returns an http:// URL nothing is listening on: a
// fresh loopback listener is opened and immediately closed, so connecting
// to it fails fast (connection refused) instead of timing out.
func unreachableProbeURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return "http://" + addr
}
