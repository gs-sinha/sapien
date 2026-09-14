package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspaces"
)

// stubSwitcher is a WorkspaceSwitcher over a fixed set of fake engines.
type stubSwitcher struct {
	engines map[string]engine.Engine
	names   map[string]string
	fail    error
}

func (s *stubSwitcher) List() []workspaces.Info {
	out := make([]workspaces.Info, 0, len(s.engines))
	for dir := range s.engines {
		out = append(out, workspaces.Info{Dir: dir, Name: s.names[dir], Open: true})
	}
	return out
}

func (s *stubSwitcher) Engine(dir string) (engine.Engine, error) {
	if s.fail != nil {
		return nil, s.fail
	}
	eng, ok := s.engines[dir]
	if !ok {
		return nil, errs.New(errs.WorkspaceNotFound, "no workspace at %s", dir)
	}
	return eng, nil
}

func newSwitchableServer(t *testing.T) (*server, *stubSwitcher) {
	t.Helper()
	primary := enginetest.New(&domain.Workspace{Version: 1, Name: "primary", Dir: "/ws/primary"})
	other := enginetest.New(&domain.Workspace{Version: 1, Name: "other", Dir: "/ws/other"})

	sw := &stubSwitcher{
		engines: map[string]engine.Engine{"/ws/primary": primary, "/ws/other": other},
		names:   map[string]string{"/ws/primary": "primary", "/ws/other": "other"},
	}
	return &server{eng: primary, switcher: sw}, sw
}

func TestSwitchWorkspace_RebindsEveryLaterCall(t *testing.T) {
	srv, _ := newSwitchableServer(t)
	require.Equal(t, "/ws/primary", srv.workspaceDir())

	res, _, err := srv.switchWorkspace(context.Background(), nil, SwitchWorkspaceInput{Dir: "/ws/other"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	got := decodeStructured[SwitchWorkspaceOutput](t, res.StructuredContent)
	assert.Equal(t, "/ws/other", got.Workspace.Dir)
	assert.Equal(t, "other", got.Workspace.Name)
	assert.Equal(t, "/ws/primary", got.Previous)

	// The binding, not just the report, moved: every tool reads engine().
	assert.Equal(t, "/ws/other", srv.workspaceDir())
}

// list_workspaces reports the name shown in its own listing, and
// switch_workspace accepts that name back, so an agent never has to
// reconstruct a path it was not given.
func TestSwitchWorkspace_AcceptsNameFromListing(t *testing.T) {
	srv, _ := newSwitchableServer(t)

	res, _, err := srv.switchWorkspace(context.Background(), nil, SwitchWorkspaceInput{Dir: "other"})
	require.NoError(t, err)
	require.False(t, res.IsError)
	assert.Equal(t, "/ws/other", srv.workspaceDir())
}

func TestSwitchWorkspace_UnknownWorkspaceKeepsCurrentBinding(t *testing.T) {
	srv, _ := newSwitchableServer(t)

	res, _, err := srv.switchWorkspace(context.Background(), nil, SwitchWorkspaceInput{Dir: "/ws/nope"})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Equal(t, "/ws/primary", srv.workspaceDir(), "a failed switch must not strand the session")
}

func TestSwitchWorkspace_RequiresDir(t *testing.T) {
	srv, _ := newSwitchableServer(t)

	res, _, err := srv.switchWorkspace(context.Background(), nil, SwitchWorkspaceInput{Dir: "  "})
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestListWorkspaces_MarksCurrent(t *testing.T) {
	srv, _ := newSwitchableServer(t)

	res, _, err := srv.listWorkspaces(context.Background(), nil, ListWorkspacesInput{})
	require.NoError(t, err)
	require.False(t, res.IsError)

	got := decodeStructured[ListWorkspacesOutput](t, res.StructuredContent)
	assert.Equal(t, "/ws/primary", got.Current)
	assert.Len(t, got.Workspaces, 2)
	assert.Contains(t, firstText(res), "* primary")
}

// Without a switcher the tools are not registered at all; calling them
// anyway (a hand-built server, or a stale client) reports why rather than
// panicking on a nil interface.
func TestWorkspaceTools_WithoutSwitcher(t *testing.T) {
	srv := &server{eng: enginetest.New(&domain.Workspace{Version: 1, Name: "only", Dir: "/ws/only"})}

	res, _, err := srv.listWorkspaces(context.Background(), nil, ListWorkspacesInput{})
	require.NoError(t, err)
	assert.True(t, res.IsError)

	res, _, err = srv.switchWorkspace(context.Background(), nil, SwitchWorkspaceInput{Dir: "/ws/other"})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Equal(t, "/ws/only", srv.workspaceDir())
}

// A failed switch must say where the session still is: the first friction
// report against Sapien was a write that landed in the previous workspace
// after a switch had failed and the agent did not notice.
func TestSwitchWorkspace_FailureSaysStillBound(t *testing.T) {
	srv, sw := newSwitchableServer(t)
	sw.fail = errs.New(errs.PermissionDenied, "bad token")

	res, _, err := srv.switchWorkspace(context.Background(), nil, SwitchWorkspaceInput{Dir: "/ws/other"})
	require.NoError(t, err)
	require.True(t, res.IsError)
	text := firstText(res)
	assert.Contains(t, text, "E_PERMISSION_DENIED")
	assert.Contains(t, text, "still bound to /ws/primary")
	assert.Equal(t, "/ws/primary", srv.workspaceDir(), "the session must not move on a failed switch")
}
