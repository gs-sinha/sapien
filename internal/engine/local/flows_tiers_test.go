package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// tierFlowYAML is a minimal valid flow with a fixed id, for the tier tests.
func tierFlowYAML(id string) string {
	return "version: 1\nid: " + id + "\nname: Tier test\nsteps:\n  - id: create\n    call: order-service.createOrder\n    body: { customerId: c1, type: STANDARD }\n"
}

// findFlow returns id's row from Flows().List, or nil.
func findFlow(t *testing.T, l *Local, id string) *domain.FlowSummary {
	t.Helper()
	list, err := l.Flows().List(context.Background(), "")
	require.NoError(t, err)
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// A flow created without naming a tier is this machine's: it lands under
// local/flows, the tier is self-ignoring from the moment it exists, and the
// catalog reports it as local-owned.
func TestFlows_CreateIn_DefaultsToLocalTier(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	f, err := l.Flows().CreateIn(ctx, tierFlowYAML("scratch"), engine.CreateFlowOptions{})
	require.NoError(t, err)
	assert.Equal(t, domain.FlowOwnerLocal, f.OwnerKind)
	assert.Empty(t, f.OwnerID)
	wantPath := filepath.Join(ws.Dir, domain.LocalDir, domain.FlowsDir, "scratch.flow.yaml")
	assert.Equal(t, wantPath, f.Path)
	assert.FileExists(t, wantPath)

	ignore, err := os.ReadFile(filepath.Join(ws.Dir, domain.LocalDir, ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, "*\n", string(ignore))

	row := findFlow(t, l, "scratch")
	require.NotNil(t, row, "the local flow must be listed")
	assert.Equal(t, domain.FlowOwnerLocal, row.OwnerKind)
	assert.Equal(t, wantPath, row.Path)

	got, err := l.Flows().Get(ctx, "scratch")
	require.NoError(t, err)
	assert.Equal(t, domain.FlowOwnerLocal, got.OwnerKind)
}

// Create keeps its pre-tier meaning: the team's flows/ directory.
func TestFlows_Create_StillWritesWorkspaceTier(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	f, err := l.Flows().Create(context.Background(), tierFlowYAML("team"), "")
	require.NoError(t, err)
	assert.Equal(t, domain.FlowOwnerWorkspace, f.OwnerKind)
	assert.Equal(t, filepath.Join(ws.Dir, domain.FlowsDir, "team.flow.yaml"), f.Path)
	_, statErr := os.Stat(filepath.Join(ws.Dir, domain.LocalDir))
	assert.True(t, os.IsNotExist(statErr), "a workspace-tier create must not create the local tier")
}

// An explicit path is relative to the chosen tier's flows directory, with
// the same safety rules Create has (no absolute, no "..").
func TestFlows_CreateIn_PathIsRelativeToTier(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	f, err := l.Flows().CreateIn(ctx, tierFlowYAML("nested"), engine.CreateFlowOptions{Path: "sub/n.flow.yaml"})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(ws.Dir, domain.LocalDir, domain.FlowsDir, "sub", "n.flow.yaml"), f.Path)
	require.NotNil(t, findFlow(t, l, "nested"))

	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("escape"), engine.CreateFlowOptions{Path: "../escape.flow.yaml"})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// The catalog keys flows by id alone, so the same id cannot live in two
// tiers; the refusal comes before any file is written.
func TestFlows_CreateIn_SameIDInAnotherTierConflicts(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().Create(ctx, tierFlowYAML("dup"), "")
	require.NoError(t, err)

	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("dup"), engine.CreateFlowOptions{})
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	assert.Contains(t, errs.As(err).Message, "workspace tier")
	_, statErr := os.Stat(filepath.Join(ws.Dir, domain.LocalDir, domain.FlowsDir, "dup.flow.yaml"))
	assert.True(t, os.IsNotExist(statErr), "nothing may be written for a refused create")
}

// The service tier is only writable when the service is read from a
// checkout this machine owns; a managed clone is reset on every sync, so
// the refusal names the way out.
func TestFlows_CreateIn_ServiceTier_ReadOnlyServiceRefused(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()
	l.readOnlyServices["order-service"] = true

	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("svc-flow"), engine.CreateFlowOptions{OwnerKind: domain.FlowOwnerService, OwnerID: "order-service"})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, errs.As(err).Hint, "sapien service bind order-service")
	assert.Nil(t, findFlow(t, l, "svc-flow"))

	// The service tier without a service, or with one the catalog does not
	// know, is refused too.
	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("svc-flow"), engine.CreateFlowOptions{OwnerKind: domain.FlowOwnerService})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("svc-flow"), engine.CreateFlowOptions{OwnerKind: domain.FlowOwnerService, OwnerID: "nope"})
	require.Error(t, err)
	assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))

	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("svc-flow"), engine.CreateFlowOptions{OwnerKind: "cloud"})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// A service bound to a writable checkout takes flows into its own
// api/flows, and the row carries the service as owner.
func TestFlows_CreateIn_ServiceTier_WritableService(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	svc, err := l.Services().Get(ctx, "order-service")
	require.NoError(t, err)

	f, err := l.Flows().CreateIn(ctx, tierFlowYAML("svc-flow"), engine.CreateFlowOptions{OwnerKind: domain.FlowOwnerService, OwnerID: "order-service"})
	require.NoError(t, err)
	assert.Equal(t, domain.FlowOwnerService, f.OwnerKind)
	assert.Equal(t, "order-service", f.OwnerID)
	assert.Equal(t, filepath.Join(svc.PackageDir, domain.FlowsDir, "svc-flow.flow.yaml"), f.Path)

	row := findFlow(t, l, "svc-flow")
	require.NotNil(t, row)
	assert.Equal(t, domain.FlowOwnerService, row.OwnerKind)
	assert.Equal(t, "order-service", row.OwnerID)
}

// Promotion moves the file, keeps the id, fixes both tiers' rows, and
// announces the change; the caller sees the flow with its new owner.
func TestFlows_Rescope_LocalToWorkspace(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Flows().CreateIn(ctx, tierFlowYAML("promote-me"), engine.CreateFlowOptions{})
	require.NoError(t, err)
	oldPath := created.Path

	ch, cancel := l.Events().Subscribe(ctx)
	defer cancel()

	moved, err := l.Flows().Rescope(ctx, "promote-me", domain.FlowOwnerWorkspace, "")
	require.NoError(t, err)
	assert.Equal(t, domain.FlowOwnerWorkspace, moved.OwnerKind)
	wantPath := filepath.Join(ws.Dir, domain.FlowsDir, "promote-me.flow.yaml")
	assert.Equal(t, wantPath, moved.Path)
	assert.FileExists(t, wantPath)
	_, statErr := os.Stat(oldPath)
	assert.True(t, os.IsNotExist(statErr), "the local copy must be gone")

	row := findFlow(t, l, "promote-me")
	require.NotNil(t, row)
	assert.Equal(t, domain.FlowOwnerWorkspace, row.OwnerKind)
	assert.Equal(t, wantPath, row.Path)
	localRows, err := l.cat.ListFlows(ctx, domain.FlowOwnerLocal, "")
	require.NoError(t, err)
	assert.Empty(t, localRows, "no local row may survive the move")

	got, err := l.Flows().Get(ctx, "promote-me")
	require.NoError(t, err)
	assert.Equal(t, domain.FlowOwnerWorkspace, got.OwnerKind)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != domain.EventFlowChanged {
				continue
			}
			sum, ok := ev.Payload.(domain.FlowSummary)
			require.True(t, ok, "flow.changed payload should be a FlowSummary, got %T", ev.Payload)
			assert.Equal(t, "promote-me", sum.ID)
			assert.Equal(t, domain.FlowOwnerWorkspace, sum.OwnerKind)
			return
		case <-deadline:
			t.Fatal("no flow.changed event after Rescope")
		}
	}
}

// A flow saved under a subdirectory keeps that relative path in its new
// tier, so the layout a developer chose survives promotion.
func TestFlows_Rescope_KeepsRelativePath(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("deep"), engine.CreateFlowOptions{Path: "smoke/deep.flow.yaml"})
	require.NoError(t, err)

	moved, err := l.Flows().Rescope(ctx, "deep", domain.FlowOwnerWorkspace, "")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(ws.Dir, domain.FlowsDir, "smoke", "deep.flow.yaml"), moved.Path)
}

// Rescope never overwrites: a file already at the destination is a
// conflict, and the source is left where it was.
func TestFlows_Rescope_ConflictAtDestination(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Flows().CreateIn(ctx, tierFlowYAML("clash"), engine.CreateFlowOptions{})
	require.NoError(t, err)

	blocker := filepath.Join(ws.Dir, domain.FlowsDir, "clash.flow.yaml")
	require.NoError(t, os.WriteFile(blocker, []byte("version: 1\nid: something-else\nsteps: []\n"), 0o644))

	_, err = l.Flows().Rescope(ctx, "clash", domain.FlowOwnerWorkspace, "")
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	assert.FileExists(t, created.Path)
	row := findFlow(t, l, "clash")
	require.NotNil(t, row)
	assert.Equal(t, domain.FlowOwnerLocal, row.OwnerKind)
}

// Moving into a read-only service is refused before anything moves.
func TestFlows_Rescope_ToReadOnlyServiceRefused(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()
	l.readOnlyServices["order-service"] = true

	created, err := l.Flows().CreateIn(ctx, tierFlowYAML("stay"), engine.CreateFlowOptions{})
	require.NoError(t, err)

	_, err = l.Flows().Rescope(ctx, "stay", domain.FlowOwnerService, "order-service")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, errs.As(err).Hint, "sapien service bind order-service")
	assert.FileExists(t, created.Path)

	_, err = l.Flows().Rescope(ctx, "does-not-exist", domain.FlowOwnerWorkspace, "")
	require.Error(t, err)
	assert.Equal(t, errs.FlowNotFound, errs.CodeOf(err))
}

// Rescoping to the tier a flow is already in changes nothing and returns
// the flow.
func TestFlows_Rescope_SameTierIsNoop(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Flows().CreateIn(ctx, tierFlowYAML("same"), engine.CreateFlowOptions{})
	require.NoError(t, err)

	same, err := l.Flows().Rescope(ctx, "same", "", "")
	require.NoError(t, err)
	assert.Equal(t, created.Path, same.Path)
	assert.Equal(t, domain.FlowOwnerLocal, same.OwnerKind)
}

// The full ladder: local -> workspace -> service (writable checkout) and
// back down, the file following each step.
func TestFlows_Rescope_ThroughServiceTierAndBack(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	svc, err := l.Services().Get(ctx, "order-service")
	require.NoError(t, err)

	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("ladder"), engine.CreateFlowOptions{})
	require.NoError(t, err)

	up, err := l.Flows().Rescope(ctx, "ladder", domain.FlowOwnerService, "order-service")
	require.NoError(t, err)
	svcPath := filepath.Join(svc.PackageDir, domain.FlowsDir, "ladder.flow.yaml")
	assert.Equal(t, svcPath, up.Path)
	assert.Equal(t, "order-service", up.OwnerID)
	row := findFlow(t, l, "ladder")
	require.NotNil(t, row)
	assert.Equal(t, domain.FlowOwnerService, row.OwnerKind)
	assert.Equal(t, "order-service", row.OwnerID)

	down, err := l.Flows().Rescope(ctx, "ladder", domain.FlowOwnerLocal, "")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(ws.Dir, domain.LocalDir, domain.FlowsDir, "ladder.flow.yaml"), down.Path)
	_, statErr := os.Stat(svcPath)
	assert.True(t, os.IsNotExist(statErr))
	row = findFlow(t, l, "ladder")
	require.NotNil(t, row)
	assert.Equal(t, domain.FlowOwnerLocal, row.OwnerKind)
	assert.Empty(t, row.OwnerID)
}

// A flow-scoped memory of a local flow stays inside the local tier, and
// promotion is what carries it into the team's memories/.
func TestFlows_Rescope_MovesFlowMemories(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Flows().CreateIn(ctx, tierFlowYAML("remembered"), engine.CreateFlowOptions{})
	require.NoError(t, err)

	mem, err := l.Memories().Create(ctx, domain.Memory{
		Text:    "createOrder needs a STANDARD type for this smoke flow.",
		Scope:   domain.ScopeFlow,
		Subject: domain.Subject{Flow: "remembered"},
	})
	require.NoError(t, err)
	localMemDir := filepath.Join(ws.Dir, domain.LocalDir, domain.MemoriesDir)
	assert.Equal(t, localMemDir, filepath.Dir(mem.FilePath), "a local flow's memory belongs to the local tier")
	assert.FileExists(t, mem.FilePath)

	_, err = l.Flows().Rescope(ctx, "remembered", domain.FlowOwnerWorkspace, "")
	require.NoError(t, err)

	got, err := l.Memories().Get(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(ws.Dir, domain.MemoriesDir), filepath.Dir(got.FilePath), "promotion moves the memory with the flow")
	assert.FileExists(t, got.FilePath)
	_, statErr := os.Stat(mem.FilePath)
	assert.True(t, os.IsNotExist(statErr), "the local copy of the memory must be gone")
	assert.Equal(t, mem.Text, got.Text)
	assert.Equal(t, domain.ScopeFlow, got.Scope)
}

// Deleting a local flow removes its file and its row.
func TestFlows_Delete_LocalFlow(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Flows().CreateIn(ctx, tierFlowYAML("gone"), engine.CreateFlowOptions{})
	require.NoError(t, err)

	require.NoError(t, l.Flows().Delete(ctx, "gone"))
	_, statErr := os.Stat(created.Path)
	assert.True(t, os.IsNotExist(statErr))
	assert.Nil(t, findFlow(t, l, "gone"))
	_, err = l.Flows().Get(ctx, "gone")
	require.Error(t, err)
	assert.Equal(t, errs.FlowNotFound, errs.CodeOf(err))
}

// Update refuses to write into a read-only service, the same way the
// create and rescope paths do.
func TestFlows_Update_ReadOnlyServiceRefused(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()
	l.readOnlyServices["allocation-service"] = true

	existing, err := l.Flows().Get(ctx, "smoke")
	require.NoError(t, err)
	_, err = l.Flows().Update(ctx, "smoke", existing.Source)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, errs.As(err).Hint, "sapien service bind allocation-service")
}

// A flow dropped into local/flows while nothing was running (an editor, a
// copy from another machine) is indexed by the next one-shot Open, exactly
// as one in flows/ is.
func TestOpen_IndexesLocalFlowsWrittenOnDisk(t *testing.T) {
	ws, _ := setupWorkspace(t)
	ctx := context.Background()

	l1, err := Open(ws, Options{})
	require.NoError(t, err)
	require.NoError(t, l1.Close())

	require.NoError(t, workspace.EnsureLocalDir(ws))
	require.NoError(t, os.WriteFile(filepath.Join(workspace.LocalFlowsDir(ws), "offline.flow.yaml"), []byte(tierFlowYAML("offline")), 0o644))

	l2, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l2.Close()
	row := findFlow(t, l2, "offline")
	require.NotNil(t, row, "a local flow written on disk must be indexed at Open")
	assert.Equal(t, domain.FlowOwnerLocal, row.OwnerKind)
	got, err := l2.Flows().Get(ctx, "offline")
	require.NoError(t, err)
	assert.Equal(t, "Tier test", got.Name)
}

// The daemon's watcher reports local/flows as the "flows" area, and the
// engine's reindex of that area covers both tiers, so an edited local flow
// reaches the catalog without a restart.
func TestWatch_LocalFlowChangedOnNewFile(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{Watch: true})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	ch, cancel := l.Events().Subscribe(ctx)
	defer cancel()

	require.NoError(t, os.WriteFile(filepath.Join(workspace.LocalFlowsDir(ws), "watched-local.flow.yaml"), []byte(tierFlowYAML("watched-local")), 0o644))

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != domain.EventFlowChanged {
				continue
			}
			row := findFlow(t, l, "watched-local")
			require.NotNil(t, row, "the new local flow must be listed after flow.changed")
			assert.Equal(t, domain.FlowOwnerLocal, row.OwnerKind)
			return
		case <-deadline:
			t.Fatal("no flow.changed event for a flow written into local/flows")
		}
	}
}
