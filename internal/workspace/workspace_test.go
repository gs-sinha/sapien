package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// writeWorkspaceFile writes raw YAML content directly, bypassing Save, so
// Load tests can control the exact on-disk shape (including invalid inputs).
func writeWorkspaceFile(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, domain.WorkspaceFileName)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestInit(t *testing.T) {
	dir := t.TempDir()

	ws, err := workspace.Init(dir, "logistics")
	require.NoError(t, err)
	require.NotNil(t, ws)

	assert.Equal(t, 1, ws.Version)
	assert.Equal(t, "logistics", ws.Name)
	assert.Equal(t, dir, ws.Dir)
	assert.Equal(t, filepath.Join(dir, domain.WorkspaceFileName), ws.File)

	// Layout on disk.
	assert.FileExists(t, filepath.Join(dir, domain.WorkspaceFileName))
	assert.DirExists(t, filepath.Join(dir, domain.FlowsDir))
	assert.DirExists(t, filepath.Join(dir, domain.MemoriesDir))
	assert.DirExists(t, filepath.Join(dir, domain.EnvironmentsDir))
	assert.FileExists(t, filepath.Join(dir, domain.EnvironmentsDir, "local.yaml"))
	assert.DirExists(t, filepath.Join(dir, domain.WorkspaceStateDir))

	gitignore, err := os.ReadFile(filepath.Join(dir, domain.WorkspaceStateDir, ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, "*\n", string(gitignore))

	env, err := workspace.LoadEnvironment(ws, "local")
	require.NoError(t, err)
	assert.Equal(t, "local", env.Name)
	assert.Equal(t, 1, env.Version)
	assert.False(t, env.Production)
}

func TestInit_DefaultsNameToDirBase(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "my-workspace")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	ws, err := workspace.Init(dir, "")
	require.NoError(t, err)
	assert.Equal(t, "my-workspace", ws.Name)
}

func TestInit_ConflictWhenAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	_, err := workspace.Init(dir, "logistics")
	require.NoError(t, err)

	_, err = workspace.Init(dir, "logistics")
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
}

func TestDiscover_FromNestedDir(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.Init(root, "logistics")
	require.NoError(t, err)
	_ = ws

	nested := filepath.Join(root, "a", "b", "c")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	found, err := workspace.Discover(nested)
	require.NoError(t, err)
	assert.Equal(t, root, found.Dir)
	assert.Equal(t, "logistics", found.Name)
}

func TestDiscover_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := workspace.Discover(dir)
	require.Error(t, err)
	assert.Equal(t, errs.WorkspaceNotFound, errs.CodeOf(err))
	e := errs.As(err)
	assert.NotEmpty(t, e.Hint)
}

func TestLoad_RoundTripPreservesOrder(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.Init(dir, "logistics")
	require.NoError(t, err)

	require.NoError(t, workspace.AddService(ws, domain.ServiceRef{
		Name:   "order-service",
		Source: domain.Source{Kind: domain.SourceLocal, Path: "../order-service"},
	}))
	require.NoError(t, workspace.AddService(ws, domain.ServiceRef{
		Name:   "rider-service",
		Source: domain.Source{Kind: domain.SourceGit, URL: "git@github.com:company/rider-service.git", Ref: "main"},
	}))
	ws.DefaultEnvironment = "local"

	require.NoError(t, workspace.Save(ws))

	raw, err := os.ReadFile(ws.File)
	require.NoError(t, err)
	content := string(raw)

	// Stable key order: version, name, services, default_environment.
	iVersion := indexOf(t, content, "version:")
	iName := indexOf(t, content, "name:")
	iServices := indexOf(t, content, "services:")
	iDefaultEnv := indexOf(t, content, "default_environment:")
	assert.True(t, iVersion < iName && iName < iServices && iServices < iDefaultEnv, "expected stable key order, got:\n%s", content)

	// 2-space indent.
	assert.Contains(t, content, "  - name: order-service")

	reloaded, err := workspace.Load(ws.File)
	require.NoError(t, err)
	assert.Equal(t, ws.Version, reloaded.Version)
	assert.Equal(t, ws.Name, reloaded.Name)
	assert.Equal(t, ws.DefaultEnvironment, reloaded.DefaultEnvironment)
	require.Len(t, reloaded.Services, 2)
	assert.Equal(t, "order-service", reloaded.Services[0].Name)
	assert.Equal(t, "rider-service", reloaded.Services[1].Name)
	assert.Equal(t, domain.SourceLocal, reloaded.Services[0].Source.Kind)
	assert.Equal(t, "../order-service", reloaded.Services[0].Source.Path)
	assert.Equal(t, domain.SourceGit, reloaded.Services[1].Source.Kind)
	assert.Equal(t, "main", reloaded.Services[1].Source.Ref)
}

func indexOf(t *testing.T, haystack, needle string) int {
	t.Helper()
	i := -1
	for idx := 0; idx+len(needle) <= len(haystack); idx++ {
		if haystack[idx:idx+len(needle)] == needle {
			i = idx
			break
		}
	}
	require.GreaterOrEqual(t, i, 0, "expected %q to appear in:\n%s", needle, haystack)
	return i
}

func TestLoad_UnsupportedVersion(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, "version: 2\nname: logistics\n")

	_, err := workspace.Load(filepath.Join(dir, domain.WorkspaceFileName))
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	e := errs.As(err)
	require.NotNil(t, e.Source)
	assert.Equal(t, 1, e.Source.Line)
}

func TestLoad_DuplicateServiceName(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, `version: 1
name: logistics
services:
  - name: order-service
    source: { type: local, path: ../order-service }
  - name: order-service
    source: { type: local, path: ../order-service-2 }
`)

	_, err := workspace.Load(filepath.Join(dir, domain.WorkspaceFileName))
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	e := errs.As(err)
	require.NotNil(t, e.Source)
	assert.Equal(t, 6, e.Source.Line) // second "name: order-service" line
}

func TestLoad_MissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := workspace.Load(filepath.Join(dir, domain.WorkspaceFileName))
	require.Error(t, err)
	assert.Equal(t, errs.WorkspaceNotFound, errs.CodeOf(err))
}

func TestLoad_ServiceMissingName(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, `version: 1
name: logistics
services:
  - source: { type: local, path: ../order-service }
`)
	_, err := workspace.Load(filepath.Join(dir, domain.WorkspaceFileName))
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestAddService_Duplicate(t *testing.T) {
	ws := &domain.Workspace{Version: 1, Name: "logistics"}
	require.NoError(t, workspace.AddService(ws, domain.ServiceRef{Name: "order-service"}))
	err := workspace.AddService(ws, domain.ServiceRef{Name: "order-service"})
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	assert.Len(t, ws.Services, 1)
}

func TestRemoveService(t *testing.T) {
	ws := &domain.Workspace{Version: 1, Name: "logistics"}
	require.NoError(t, workspace.AddService(ws, domain.ServiceRef{Name: "order-service"}))
	require.NoError(t, workspace.AddService(ws, domain.ServiceRef{Name: "rider-service"}))

	require.NoError(t, workspace.RemoveService(ws, "order-service"))
	require.Len(t, ws.Services, 1)
	assert.Equal(t, "rider-service", ws.Services[0].Name)
}

func TestRemoveService_Missing(t *testing.T) {
	ws := &domain.Workspace{Version: 1, Name: "logistics"}
	err := workspace.RemoveService(ws, "nope")
	require.Error(t, err)
	assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
}

// TestSetTeamRef_UpdatesCommittedFile: team-scope SetRef rewrites
// source.ref in sapien.workspace.yaml, preserving everything else.
func TestSetTeamRef_UpdatesCommittedFile(t *testing.T) {
	ws, err := workspace.Init(t.TempDir(), "team")
	require.NoError(t, err)
	require.NoError(t, workspace.AddService(ws, domain.ServiceRef{
		Name:   "rider-service",
		Source: domain.Source{Kind: domain.SourceGit, URL: "git@github.com:acme/rider-service.git", Ref: "main", Subdir: "api"},
	}))
	require.NoError(t, workspace.Save(ws))

	require.NoError(t, workspace.SetTeamRef(ws, "rider-service", "release-2"))
	require.NoError(t, workspace.Save(ws))

	raw, err := os.ReadFile(ws.File)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "ref: release-2")
	assert.Contains(t, string(raw), "subdir: api", "everything else in the entry survives")

	reloaded, err := workspace.Load(ws.File)
	require.NoError(t, err)
	assert.Equal(t, "release-2", reloaded.Services[0].Source.Ref)
}

// TestSetTeamRef_WithLocalOverride_UpdatesTeamNotEffective: when this
// machine currently overrides the service with a local checkout, team-scope
// SetRef still updates the committed ref (ref.Team), leaving the effective
// (bound) source alone.
func TestSetTeamRef_WithLocalOverride_UpdatesTeamNotEffective(t *testing.T) {
	ws, err := workspace.Init(t.TempDir(), "team")
	require.NoError(t, err)
	require.NoError(t, workspace.AddService(ws, domain.ServiceRef{
		Name:   "rider-service",
		Source: domain.Source{Kind: domain.SourceGit, URL: "git@github.com:acme/rider-service.git", Ref: "main"},
	}))
	require.NoError(t, workspace.Save(ws))
	require.NoError(t, workspace.Bind(ws, "rider-service", "/tmp/rider-service"))

	require.NoError(t, workspace.SetTeamRef(ws, "rider-service", "release-2"))

	ref := ws.Services[0]
	assert.Equal(t, domain.SourceLocal, ref.Source.Kind, "the bound checkout keeps reading, unaffected")
	require.NotNil(t, ref.Team)
	assert.Equal(t, "release-2", ref.Team.Ref)

	require.NoError(t, workspace.Save(ws))
	raw, err := os.ReadFile(ws.File)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "ref: release-2")
}

// TestSetTeamRef_RejectsNonGitService: a ref only means something for a
// git source.
func TestSetTeamRef_RejectsNonGitService(t *testing.T) {
	ws := &domain.Workspace{Version: 1, Name: "logistics"}
	require.NoError(t, workspace.AddService(ws, domain.ServiceRef{
		Name:   "rider-service",
		Source: domain.Source{Kind: domain.SourceLocal, Path: "/tmp/rider-service"},
	}))
	err := workspace.SetTeamRef(ws, "rider-service", "release-2")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// TestSetTeamRef_Missing: an unregistered service is refused.
func TestSetTeamRef_Missing(t *testing.T) {
	ws := &domain.Workspace{Version: 1, Name: "logistics"}
	err := workspace.SetTeamRef(ws, "nope", "release-2")
	require.Error(t, err)
	assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
}

func TestResolveSourcePath_Relative(t *testing.T) {
	root := t.TempDir()
	wsDir := filepath.Join(root, "workspace")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	svcDir := filepath.Join(root, "order-service")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))

	ws := &domain.Workspace{Dir: wsDir}
	resolved, err := workspace.ResolveSourcePath(ws, domain.Source{Kind: domain.SourceLocal, Path: "../order-service"})
	require.NoError(t, err)

	wantAbs, err := filepath.Abs(svcDir)
	require.NoError(t, err)
	assert.Equal(t, wantAbs, resolved)
}

func TestResolveSourcePath_Tilde(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	svcDir := filepath.Join(home, ".sapien-test-resolve-source")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	t.Cleanup(func() { _ = os.RemoveAll(svcDir) })

	ws := &domain.Workspace{Dir: t.TempDir()}
	resolved, err := workspace.ResolveSourcePath(ws, domain.Source{Kind: domain.SourceLocal, Path: "~/.sapien-test-resolve-source"})
	require.NoError(t, err)
	assert.Equal(t, svcDir, resolved)
}

func TestResolveSourcePath_NotExist(t *testing.T) {
	ws := &domain.Workspace{Dir: t.TempDir()}
	_, err := workspace.ResolveSourcePath(ws, domain.Source{Kind: domain.SourceLocal, Path: "does-not-exist"})
	require.Error(t, err)
	assert.Equal(t, errs.ServiceSource, errs.CodeOf(err))
}

func TestResolveSourcePath_NonLocalRejected(t *testing.T) {
	ws := &domain.Workspace{Dir: t.TempDir()}
	_, err := workspace.ResolveSourcePath(ws, domain.Source{Kind: domain.SourceGit, URL: "git@example.com:x/y.git"})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestStateDirDBPathEnsureStateDir(t *testing.T) {
	dir := t.TempDir()
	ws := &domain.Workspace{Dir: dir}

	assert.Equal(t, filepath.Join(dir, domain.WorkspaceStateDir), workspace.StateDir(ws))
	assert.Equal(t, filepath.Join(dir, domain.WorkspaceStateDir, domain.WorkspaceDBFile), workspace.DBPath(ws))

	require.NoError(t, workspace.EnsureStateDir(ws))
	assert.DirExists(t, workspace.StateDir(ws))
}
