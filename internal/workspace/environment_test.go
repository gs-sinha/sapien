package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/workspace"
)

func newTestWorkspace(t *testing.T) *domain.Workspace {
	t.Helper()
	dir := t.TempDir()
	ws, err := workspace.Init(dir, "logistics")
	require.NoError(t, err)
	return ws
}

func TestListEnvironments(t *testing.T) {
	ws := newTestWorkspace(t)

	staging := &domain.Environment{Version: 1, Name: "staging", Production: false}
	require.NoError(t, workspace.SaveEnvironment(ws, staging))

	envs, err := workspace.ListEnvironments(ws)
	require.NoError(t, err)
	require.Len(t, envs, 2) // local (from Init) + staging

	names := []string{envs[0].Name, envs[1].Name}
	assert.Equal(t, []string{"local", "staging"}, names) // sorted

	for _, e := range envs {
		assert.NotEmpty(t, e.Path)
		assert.FileExists(t, e.Path)
	}
}

func TestListEnvironments_NoDir(t *testing.T) {
	ws := &domain.Workspace{Dir: t.TempDir()}
	envs, err := workspace.ListEnvironments(ws)
	require.NoError(t, err)
	assert.Empty(t, envs)
}

func TestLoadEnvironment(t *testing.T) {
	ws := newTestWorkspace(t)

	env, err := workspace.LoadEnvironment(ws, "local")
	require.NoError(t, err)
	assert.Equal(t, "local", env.Name)
	assert.Equal(t, 1, env.Version)
	assert.False(t, env.Production)
	assert.Equal(t, filepath.Join(ws.Dir, domain.EnvironmentsDir, "local.yaml"), env.Path)
}

func TestLoadEnvironment_NotFound(t *testing.T) {
	ws := newTestWorkspace(t)
	_, err := workspace.LoadEnvironment(ws, "staging")
	require.Error(t, err)
	assert.Equal(t, errs.EnvNotFound, errs.CodeOf(err))
}

func TestSaveEnvironment_RoundTrip(t *testing.T) {
	ws := newTestWorkspace(t)

	env := &domain.Environment{
		Version:    1,
		Name:       "staging",
		Production: true,
		Services: map[string]domain.ServiceEnv{
			"order-service": {BaseURL: "https://orders.staging.internal"},
		},
		Vars: map[string]string{"TEST_CUSTOMER": "cust_123"},
	}
	require.NoError(t, workspace.SaveEnvironment(ws, env))
	assert.Equal(t, filepath.Join(ws.Dir, domain.EnvironmentsDir, "staging.yaml"), env.Path)

	reloaded, err := workspace.LoadEnvironment(ws, "staging")
	require.NoError(t, err)
	assert.Equal(t, env.Name, reloaded.Name)
	assert.True(t, reloaded.Production)
	assert.Equal(t, "https://orders.staging.internal", reloaded.Services["order-service"].BaseURL)
	assert.Equal(t, "cust_123", reloaded.Vars["TEST_CUSTOMER"])

	data, err := os.ReadFile(env.Path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "  order-service:")
	assert.Contains(t, string(data), "    base_url: https://orders.staging.internal")
}

func TestDefaultEnvironment_ExplicitlySet(t *testing.T) {
	ws := newTestWorkspace(t)
	ws.DefaultEnvironment = "staging"
	assert.Equal(t, "staging", workspace.DefaultEnvironment(ws))
}

func TestDefaultEnvironment_FallsBackToLocal(t *testing.T) {
	ws := newTestWorkspace(t)
	assert.Equal(t, "local", workspace.DefaultEnvironment(ws))
}

func TestDefaultEnvironment_OnlyEnvWhenNoLocal(t *testing.T) {
	dir := t.TempDir()
	ws := &domain.Workspace{Dir: dir}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, domain.EnvironmentsDir), 0o755))

	env := &domain.Environment{Version: 1, Name: "staging"}
	require.NoError(t, workspace.SaveEnvironment(ws, env))

	assert.Equal(t, "staging", workspace.DefaultEnvironment(ws))
}

func TestDefaultEnvironment_Empty(t *testing.T) {
	ws := &domain.Workspace{Dir: t.TempDir()}
	assert.Equal(t, "", workspace.DefaultEnvironment(ws))
}
