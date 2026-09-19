package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// isolateUserConfig points $SAPIEN_CONFIG at a file under a fresh temp
// directory for the duration of the test, so no test ever reads (or
// depends on the absence of) the real developer's ~/.sapien/config.yaml.
func isolateUserConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if contents != "" {
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	}
	t.Setenv("SAPIEN_CONFIG", path)
	return path
}

func newTestWorkspace(t *testing.T) *domain.Workspace {
	t.Helper()
	dir := t.TempDir()
	return &domain.Workspace{Dir: dir, File: filepath.Join(dir, domain.WorkspaceFileName)}
}

func TestDefaults(t *testing.T) {
	d := config.Defaults()
	assert.Equal(t, "10m", d.Git.SyncInterval)
	assert.Equal(t, "30m", d.Daemon.IdleTimeout)
	assert.Equal(t, "", d.Git.Timeout)
	assert.Equal(t, "", d.Git.CacheDir)
	assert.False(t, d.Semantic.Enabled)

	assert.Equal(t, 10*time.Minute, d.Git.SyncIntervalDuration())
	assert.Equal(t, time.Duration(0), d.Git.TimeoutDuration())
	assert.Equal(t, 30*time.Minute, d.Daemon.IdleTimeoutDuration())
}

func TestDurationAccessors_FallBackOnEmptyOrInvalid(t *testing.T) {
	var g config.Git
	assert.Equal(t, 10*time.Minute, g.SyncIntervalDuration())
	assert.Equal(t, time.Duration(0), g.TimeoutDuration())

	g = config.Git{SyncInterval: "not-a-duration", Timeout: "also-bad"}
	assert.Equal(t, 10*time.Minute, g.SyncIntervalDuration())
	assert.Equal(t, time.Duration(0), g.TimeoutDuration())

	var d config.Daemon
	assert.Equal(t, 30*time.Minute, d.IdleTimeoutDuration())

	g = config.Git{SyncInterval: "5m", Timeout: "45s"}
	assert.Equal(t, 5*time.Minute, g.SyncIntervalDuration())
	assert.Equal(t, 45*time.Second, g.TimeoutDuration())
}

func TestLoad_NoFiles_ReturnsDefaults(t *testing.T) {
	isolateUserConfig(t, "")

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, config.Defaults(), cfg)
}

func TestLoad_UserLevelOnly(t *testing.T) {
	isolateUserConfig(t, `
semantic:
  enabled: true
  kind: ollama
  base_url: http://localhost:11434
  model: nomic-embed-text
  batch_size: 16
git:
  cache_dir: /tmp/sapien-repos
  sync_interval: 5m
daemon:
  idle_timeout: 15m
`)

	cfg, err := config.Load(nil)
	require.NoError(t, err)

	assert.True(t, cfg.Semantic.Enabled)
	assert.Equal(t, "ollama", cfg.Semantic.Kind)
	assert.Equal(t, "http://localhost:11434", cfg.Semantic.BaseURL)
	assert.Equal(t, "nomic-embed-text", cfg.Semantic.Model)
	assert.Equal(t, 16, cfg.Semantic.BatchSize)
	assert.Equal(t, "/tmp/sapien-repos", cfg.Git.CacheDir)
	assert.Equal(t, 5*time.Minute, cfg.Git.SyncIntervalDuration())
	assert.Equal(t, 15*time.Minute, cfg.Daemon.IdleTimeoutDuration())
}

func TestLoad_WorkspaceOverridesUser(t *testing.T) {
	isolateUserConfig(t, `
semantic:
  enabled: true
  kind: openai
  model: text-embedding-3-small
git:
  sync_interval: 5m
`)

	ws := newTestWorkspace(t)
	require.NoError(t, os.MkdirAll(filepath.Join(ws.Dir, domain.WorkspaceStateDir), 0o755))
	require.NoError(t, os.WriteFile(config.WorkspacePath(ws), []byte(`
git:
  sync_interval: 2m
`), 0o644))

	cfg, err := config.Load(ws)
	require.NoError(t, err)

	// Workspace wins on the field it sets.
	assert.Equal(t, 2*time.Minute, cfg.Git.SyncIntervalDuration())
	// Fields the workspace file never mentions still come from the user
	// level, not reset to zero.
	assert.True(t, cfg.Semantic.Enabled)
	assert.Equal(t, "openai", cfg.Semantic.Kind)
	assert.Equal(t, "text-embedding-3-small", cfg.Semantic.Model)
}

func TestLoad_WorkspaceNilMeansUserLevelOnly(t *testing.T) {
	isolateUserConfig(t, `
daemon:
  idle_timeout: 1h
`)

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, time.Hour, cfg.Daemon.IdleTimeoutDuration())
}

func TestLoad_MissingWorkspaceFile(t *testing.T) {
	isolateUserConfig(t, `
git:
  sync_interval: 3m
`)
	ws := newTestWorkspace(t) // no .sapien/config.yaml written

	cfg, err := config.Load(ws)
	require.NoError(t, err)
	assert.Equal(t, 3*time.Minute, cfg.Git.SyncIntervalDuration())
}

func TestLoad_UnknownTopLevelKeysIgnored(t *testing.T) {
	isolateUserConfig(t, `
mcp:
  default:
    read_contracts: false
semantic:
  enabled: true
  kind: openai
  model: m
some_future_key:
  nested: true
`)

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.True(t, cfg.Semantic.Enabled)
	assert.Equal(t, "openai", cfg.Semantic.Kind)
}

func TestLoad_BadDuration_GitSyncInterval(t *testing.T) {
	isolateUserConfig(t, `
git:
  sync_interval: banana
`)

	_, err := config.Load(nil)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	e := errs.As(err)
	assert.Equal(t, "git.sync_interval", e.Details["key"])
}

func TestLoad_BadDuration_GitTimeout(t *testing.T) {
	isolateUserConfig(t, `
git:
  timeout: nope
`)

	_, err := config.Load(nil)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	e := errs.As(err)
	assert.Equal(t, "git.timeout", e.Details["key"])
}

func TestLoad_BadDuration_DaemonIdleTimeout(t *testing.T) {
	isolateUserConfig(t, `
daemon:
  idle_timeout: whenever
`)

	_, err := config.Load(nil)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	e := errs.As(err)
	assert.Equal(t, "daemon.idle_timeout", e.Details["key"])
}

func TestLoad_BadDuration_InWorkspaceFile(t *testing.T) {
	isolateUserConfig(t, "")
	ws := newTestWorkspace(t)
	require.NoError(t, os.MkdirAll(filepath.Join(ws.Dir, domain.WorkspaceStateDir), 0o755))
	require.NoError(t, os.WriteFile(config.WorkspacePath(ws), []byte(`
daemon:
  idle_timeout: not-a-duration
`), 0o644))

	_, err := config.Load(ws)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestLoad_MalformedYAML(t *testing.T) {
	isolateUserConfig(t, "not: valid: yaml: [")

	_, err := config.Load(nil)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestUserPath_EnvOverride(t *testing.T) {
	t.Setenv("SAPIEN_CONFIG", "/custom/path/config.yaml")
	assert.Equal(t, "/custom/path/config.yaml", config.UserPath())
}

func TestUserPath_DefaultsUnderHome(t *testing.T) {
	t.Setenv("SAPIEN_CONFIG", "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".sapien", "config.yaml"), config.UserPath())
}

func TestWorkspacePath(t *testing.T) {
	ws := newTestWorkspace(t)
	assert.Equal(t, filepath.Join(ws.Dir, ".sapien", "config.yaml"), config.WorkspacePath(ws))
}

// --- friction ---

func TestDefaults_Friction(t *testing.T) {
	d := config.Defaults()
	assert.Equal(t, "gs-sinha/sapien", d.Friction.Repo)
	assert.Equal(t, "General", d.Friction.Category)
}

// A partial friction: file overrides only the field it sets, exactly like
// every other section (TestLoad_WorkspaceOverridesUser above): category is
// set, repo is not mentioned and stays at its default.
func TestLoad_Friction_PartialOverride(t *testing.T) {
	isolateUserConfig(t, `
friction:
  category: "Agent Feedback"
`)

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, "Agent Feedback", cfg.Friction.Category)
	assert.Equal(t, "gs-sinha/sapien", cfg.Friction.Repo) // untouched default
}

func TestLoad_Friction_RepoOverride(t *testing.T) {
	isolateUserConfig(t, `
friction:
  repo: acme/internal-tools
`)

	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, "acme/internal-tools", cfg.Friction.Repo)
	assert.Equal(t, "General", cfg.Friction.Category) // untouched default
}

// --- Updates (PLAN §34f item 4) ---

func TestUpdates_Enabled_DefaultsTrue(t *testing.T) {
	assert.True(t, config.Updates{}.Enabled(), "a config that never mentions updates: must default to checking")
}

func TestUpdates_Enabled_ExplicitFalse(t *testing.T) {
	f := false
	assert.False(t, config.Updates{Check: &f}.Enabled())
}

func TestUpdates_Enabled_ExplicitTrue(t *testing.T) {
	tr := true
	assert.True(t, config.Updates{Check: &tr}.Enabled())
}

func TestUpdates_Enabled_EnvOverridesConfigTrue(t *testing.T) {
	t.Setenv("SAPIEN_NO_UPDATE_CHECK", "1")
	tr := true
	assert.False(t, config.Updates{Check: &tr}.Enabled(), "the env escape hatch must win even over an explicit check: true")
}

func TestLoad_Updates_Default_IsEnabled(t *testing.T) {
	isolateUserConfig(t, "")
	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.True(t, cfg.Updates.Enabled())
}

func TestLoad_Updates_CheckFalse(t *testing.T) {
	isolateUserConfig(t, `
updates:
  check: false
`)
	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.False(t, cfg.Updates.Enabled())
}

func TestLoad_Updates_WorkspaceOverridesUser(t *testing.T) {
	isolateUserConfig(t, `
updates:
  check: false
`)
	ws := newTestWorkspace(t)
	require.NoError(t, os.MkdirAll(filepath.Join(ws.Dir, domain.WorkspaceStateDir), 0o755))
	require.NoError(t, os.WriteFile(config.WorkspacePath(ws), []byte(`
updates:
  check: true
`), 0o644))

	cfg, err := config.Load(ws)
	require.NoError(t, err)
	assert.True(t, cfg.Updates.Enabled())
}

// --- UserDir ---

func TestUserDir_UnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	assert.Equal(t, filepath.Join(home, ".sapien"), config.UserDir())
}

func TestUserDir_IgnoresSAPIEN_CONFIG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SAPIEN_CONFIG", "/custom/path/config.yaml")
	assert.Equal(t, filepath.Join(home, ".sapien"), config.UserDir(), "UserDir must not follow SAPIEN_CONFIG, which only relocates config.yaml itself")
}

func TestLoad_Friction_WorkspaceOverridesUser(t *testing.T) {
	isolateUserConfig(t, `
friction:
  repo: acme/user-level
  category: user-level-category
`)

	ws := newTestWorkspace(t)
	require.NoError(t, os.MkdirAll(filepath.Join(ws.Dir, domain.WorkspaceStateDir), 0o755))
	require.NoError(t, os.WriteFile(config.WorkspacePath(ws), []byte(`
friction:
  category: workspace-level-category
`), 0o644))

	cfg, err := config.Load(ws)
	require.NoError(t, err)
	// Workspace wins on the field it sets.
	assert.Equal(t, "workspace-level-category", cfg.Friction.Category)
	// The field the workspace file never mentions still comes from the
	// user level, not reset to the default.
	assert.Equal(t, "acme/user-level", cfg.Friction.Repo)
}
