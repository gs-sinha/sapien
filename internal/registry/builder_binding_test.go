package registry_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/registry"
)

// A local source that is a git checkout (a developer's own clone, not a
// managed one) is described by git: the binding says which branch and
// commit this machine reads, so a UI can show it beside the team source.
func TestBuilder_Build_Binding_LocalCheckout(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	srcAPI := filepath.Join(fixturesRoot(t), "order-service", "api")
	sha := pushDir(t, bareDir, env, srcAPI, "seed order-service")

	clone := t.TempDir()
	runGit(t, "", env, "clone", bareDir, clone)
	runGit(t, clone, env, "checkout", "-b", "feature/orders")

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: t.TempDir()}
	b := registry.NewBuilder(ws).WithGit(gitManager(t, env))

	snap, err := b.Build(context.Background(), domain.ServiceRef{
		Name:   "order-service",
		Source: domain.Source{Kind: domain.SourceLocal, Path: clone},
	})
	require.NoError(t, err)

	binding := snap.Service.Binding
	require.NotNil(t, binding)
	assert.Equal(t, domain.BindingLocal, binding.Mode)
	assert.True(t, binding.Writable)
	assert.Nil(t, binding.Team, "a committed local source has no team source")
	require.NotNil(t, binding.Local)
	assert.Equal(t, clone, binding.Local.Path)
	assert.Equal(t, "feature/orders", binding.Local.Branch)
	assert.Equal(t, sha, binding.Local.Commit)
	assert.Equal(t, bareDir, binding.Local.Remote)
	assert.Equal(t, 0, binding.Local.Dirty)

	// A per-machine override of a git source carries the committed source
	// through as Team.
	team := domain.Source{Kind: domain.SourceGit, URL: url, Ref: "main"}
	snap, err = b.Build(context.Background(), domain.ServiceRef{
		Name:   "order-service",
		Source: domain.Source{Kind: domain.SourceLocal, Path: clone},
		Team:   &team,
	})
	require.NoError(t, err)
	require.NotNil(t, snap.Service.Binding)
	assert.Equal(t, domain.BindingLocal, snap.Service.Binding.Mode)
	require.NotNil(t, snap.Service.Binding.Team)
	assert.Equal(t, team, *snap.Service.Binding.Team)
	assert.Equal(t, "feature/orders", snap.Service.Binding.Local.Branch)
}

// Without a git manager, a local source is still bound -- just not
// described beyond its path.
func TestBuilder_Build_Binding_LocalWithoutGit(t *testing.T) {
	ws := fixtureWorkspace(t)
	snap, err := registry.NewBuilder(ws).Build(context.Background(), fixtureRef("order-service"))
	require.NoError(t, err)

	binding := snap.Service.Binding
	require.NotNil(t, binding)
	assert.Equal(t, domain.BindingLocal, binding.Mode)
	assert.True(t, binding.Writable)
	require.NotNil(t, binding.Local)
	assert.Equal(t, filepath.Join(ws.Dir, "order-service"), binding.Local.Path)
	assert.Empty(t, binding.Local.Branch)
}

func TestBuilder_Build_Binding_GitSourceIsTeam(t *testing.T) {
	env := hermeticGitEnv(t)
	bareDir, url := newBareRepo(t, env)
	pushDir(t, bareDir, env, filepath.Join(fixturesRoot(t), "order-service", "api"), "seed")

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: t.TempDir()}
	src := domain.Source{Kind: domain.SourceGit, URL: url, Ref: "main"}
	snap, err := registry.NewBuilder(ws).WithGit(gitManager(t, env)).Build(context.Background(), domain.ServiceRef{
		Name:   "order-service",
		Source: src,
	})
	require.NoError(t, err)

	binding := snap.Service.Binding
	require.NotNil(t, binding)
	assert.Equal(t, domain.BindingTeam, binding.Mode)
	assert.False(t, binding.Writable)
	assert.Nil(t, binding.Local)
	require.NotNil(t, binding.Team)
	assert.Equal(t, src, *binding.Team)
}
