package workspace_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// gitRef is the committed shape every binding test starts from: a team
// service read from a managed clone.
func gitRef(name string) domain.ServiceRef {
	return domain.ServiceRef{
		Name:   name,
		Source: domain.Source{Kind: domain.SourceGit, URL: "git@github.com:acme/" + name + ".git", Ref: "main", Contract: "openapi.yaml"},
	}
}

func initWithGitService(t *testing.T) *domain.Workspace {
	t.Helper()
	ws, err := workspace.Init(t.TempDir(), "team")
	require.NoError(t, err)
	require.NoError(t, workspace.AddService(ws, gitRef("rider-service")))
	require.NoError(t, workspace.Save(ws))
	return ws
}

func TestLocalOverride_BindSaveLoadRoundTrip(t *testing.T) {
	ws := initWithGitService(t)

	require.NoError(t, workspace.Bind(ws, "rider-service", "~/code/rider-service"))
	require.NoError(t, workspace.SaveLocal(ws))
	assert.FileExists(t, workspace.LocalOverridePath(ws))

	reloaded, err := workspace.Load(ws.File)
	require.NoError(t, err)
	require.Len(t, reloaded.Services, 1)
	ref := reloaded.Services[0]

	// Load reports the effective source and keeps the committed one.
	assert.Equal(t, domain.SourceLocal, ref.Source.Kind)
	assert.Equal(t, "~/code/rider-service", ref.Source.Path)
	assert.Equal(t, "openapi.yaml", ref.Source.Contract, "the committed contract carries over when the override names none")
	require.NotNil(t, ref.Team)
	assert.Equal(t, domain.SourceGit, ref.Team.Kind)
	assert.Equal(t, "git@github.com:acme/rider-service.git", ref.Team.URL)
	assert.Equal(t, "main", ref.Team.Ref)
}

// The critical invariant: a Save after a Load that applied an override
// writes the committed source, never the override.
func TestLocalOverride_SaveAfterLoadWritesCommittedSource(t *testing.T) {
	ws := initWithGitService(t)
	require.NoError(t, workspace.Bind(ws, "rider-service", "/tmp/rider-service"))
	require.NoError(t, workspace.SaveLocal(ws))

	reloaded, err := workspace.Load(ws.File)
	require.NoError(t, err)
	reloaded.DefaultEnvironment = "local" // any edit a normal command might make
	require.NoError(t, workspace.Save(reloaded))

	raw, err := os.ReadFile(ws.File)
	require.NoError(t, err)
	content := string(raw)
	assert.Contains(t, content, "type: git")
	assert.Contains(t, content, "git@github.com:acme/rider-service.git")
	assert.NotContains(t, content, "/tmp/rider-service")
	assert.NotContains(t, content, "type: local")

	// The override is still there for the next Load.
	again, err := workspace.Load(ws.File)
	require.NoError(t, err)
	assert.Equal(t, domain.SourceLocal, again.Services[0].Source.Kind)
	assert.Equal(t, "/tmp/rider-service", again.Services[0].Source.Path)
}

func TestLocalOverride_ExplicitContract(t *testing.T) {
	ws := initWithGitService(t)
	require.NoError(t, os.WriteFile(workspace.LocalOverridePath(ws), []byte(`version: 1
services:
  rider-service:
    path: ../rider-service
    contract: api/spec.yaml
`), 0o644))

	reloaded, err := workspace.Load(ws.File)
	require.NoError(t, err)
	assert.Equal(t, "api/spec.yaml", reloaded.Services[0].Source.Contract)
	assert.Equal(t, "openapi.yaml", reloaded.Services[0].Team.Contract)

	// SaveLocal keeps a contract that differs from the committed one.
	require.NoError(t, workspace.SaveLocal(reloaded))
	raw, err := os.ReadFile(workspace.LocalOverridePath(ws))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "contract: api/spec.yaml")
}

func TestLocalOverride_UnknownServiceIgnored(t *testing.T) {
	ws := initWithGitService(t)
	require.NoError(t, os.WriteFile(workspace.LocalOverridePath(ws), []byte(`version: 1
services:
  rider-service:
    path: /tmp/rider-service
  gone:
    path: /tmp/gone
`), 0o644))

	reloaded, err := workspace.Load(ws.File)
	require.NoError(t, err)
	require.Len(t, reloaded.Services, 1)
	assert.Equal(t, "/tmp/rider-service", reloaded.Services[0].Source.Path)

	// SaveLocal drops the entry for the service that is no longer registered.
	require.NoError(t, workspace.SaveLocal(reloaded))
	raw, err := os.ReadFile(workspace.LocalOverridePath(ws))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "gone")
	assert.Contains(t, string(raw), "rider-service")
}

func TestLocalOverride_ParseErrorNamesFile(t *testing.T) {
	ws := initWithGitService(t)
	require.NoError(t, os.WriteFile(workspace.LocalOverridePath(ws), []byte("services: [not a map\n"), 0o644))

	_, err := workspace.Load(ws.File)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, err.Error(), domain.WorkspaceLocalFileName)
}

func TestLocalOverride_UnbindRemovesFileWhenEmpty(t *testing.T) {
	ws := initWithGitService(t)
	require.NoError(t, workspace.Bind(ws, "rider-service", "/tmp/rider-service"))
	require.NoError(t, workspace.SaveLocal(ws))
	require.FileExists(t, workspace.LocalOverridePath(ws))

	require.NoError(t, workspace.Unbind(ws, "rider-service"))
	assert.Nil(t, ws.Services[0].Team)
	assert.Equal(t, domain.SourceGit, ws.Services[0].Source.Kind)
	assert.Equal(t, "git@github.com:acme/rider-service.git", ws.Services[0].Source.URL)

	require.NoError(t, workspace.SaveLocal(ws))
	assert.NoFileExists(t, workspace.LocalOverridePath(ws))

	// Unbinding a service with no override is a no-op, not an error.
	require.NoError(t, workspace.Unbind(ws, "rider-service"))
	require.NoError(t, workspace.SaveLocal(ws))
	assert.NoFileExists(t, workspace.LocalOverridePath(ws))
}

func TestLocalOverride_BindTwiceKeepsOriginalTeam(t *testing.T) {
	ws := initWithGitService(t)
	require.NoError(t, workspace.Bind(ws, "rider-service", "/tmp/one"))
	require.NoError(t, workspace.Bind(ws, "rider-service", "/tmp/two"))

	ref := ws.Services[0]
	assert.Equal(t, "/tmp/two", ref.Source.Path)
	require.NotNil(t, ref.Team)
	assert.Equal(t, domain.SourceGit, ref.Team.Kind, "the second bind must not turn the first override into the team source")
}

func TestLocalOverride_BindUnknownService(t *testing.T) {
	ws := initWithGitService(t)
	err := workspace.Bind(ws, "nope", "/tmp/x")
	require.Error(t, err)
	assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))

	err = workspace.Unbind(ws, "nope")
	require.Error(t, err)
	assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
}

func TestEnsureLocalIgnored(t *testing.T) {
	ws, err := workspace.Init(t.TempDir(), "team")
	require.NoError(t, err)
	gitignore := filepath.Join(ws.Dir, ".gitignore")

	// Init already wrote it: the override file and the state directory.
	raw, err := os.ReadFile(gitignore)
	require.NoError(t, err)
	assert.Equal(t, domain.WorkspaceLocalFileName+"\n.sapien/\n", string(raw))

	// Idempotent.
	require.NoError(t, workspace.EnsureLocalIgnored(ws))
	raw, err = os.ReadFile(gitignore)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(raw), domain.WorkspaceLocalFileName))
	assert.Equal(t, 1, strings.Count(string(raw), ".sapien"))

	// Appends to an existing file that lacks a trailing newline.
	require.NoError(t, os.WriteFile(gitignore, []byte("node_modules"), 0o644))
	require.NoError(t, workspace.EnsureLocalIgnored(ws))
	raw, err = os.ReadFile(gitignore)
	require.NoError(t, err)
	assert.Equal(t, "node_modules\n"+domain.WorkspaceLocalFileName+"\n.sapien/\n", string(raw))

	// Recognises the usual spellings, and adds only what is missing: an
	// existing workspace that ignored one of them gets the other.
	for _, existing := range []string{"/" + domain.WorkspaceLocalFileName + "\n/.sapien\n", domain.WorkspaceLocalFileName + "\n.sapien\n"} {
		require.NoError(t, os.WriteFile(gitignore, []byte(existing), 0o644))
		require.NoError(t, workspace.EnsureLocalIgnored(ws))
		raw, err = os.ReadFile(gitignore)
		require.NoError(t, err)
		assert.Equal(t, existing, string(raw))
	}
	require.NoError(t, os.WriteFile(gitignore, []byte(domain.WorkspaceLocalFileName+"\n"), 0o644))
	require.NoError(t, workspace.EnsureLocalIgnored(ws))
	raw, err = os.ReadFile(gitignore)
	require.NoError(t, err)
	assert.Equal(t, domain.WorkspaceLocalFileName+"\n.sapien/\n", string(raw))

	// Missing file: created.
	require.NoError(t, os.Remove(gitignore))
	require.NoError(t, workspace.EnsureLocalIgnored(ws))
	assert.FileExists(t, gitignore)
}
