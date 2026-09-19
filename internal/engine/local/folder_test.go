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
	"github.com/gs-sinha/sapien/internal/memory"
)

// TestWatch_MemoryChangedOnNewNestedFile confirms the file watcher picks up
// a memory file dropped directly into a brand-new nested subdirectory (PLAN
// §34f item 6: a memory saved into a subfolder is indexed exactly like one
// at the root), not just a new file at the root of an already-watched
// directory. The file is written by hand (memory.Format), not through
// Memories().Create, so only the watcher -- registry.Watcher dynamically
// extending its watch tree when a directory is created inside one it's
// already watching (internal/registry/watch.go's handleEvent), plus
// internal/memory.Locator.Files now walking recursively -- can be what
// finds it.
func TestWatch_MemoryChangedOnNewNestedFile(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{Watch: true})
	require.NoError(t, err)
	defer l.Close()

	ctx := context.Background()

	nestedDir := filepath.Join(ws.Dir, domain.MemoriesDir, "incidents", "2026-09")
	require.NoError(t, os.MkdirAll(nestedDir, 0o755))

	m := domain.Memory{
		ID:      "mem_01J8Z5K3W2RQ4X7M9NPQR2T3ZZ",
		Scope:   domain.ScopeWorkspace,
		Created: time.Now().UTC(),
		Updated: time.Now().UTC(),
		Text:    "nested memory dropped by hand",
	}
	require.NoError(t, os.WriteFile(filepath.Join(nestedDir, memory.FileName(m.ID)), memory.Format(m), 0o644))

	assert.Eventually(t, func() bool {
		got, err := l.Memories().Get(ctx, m.ID)
		return err == nil && got.Folder == "incidents/2026-09"
	}, 5*time.Second, 100*time.Millisecond, "expected the watcher to index a memory dropped into a brand-new nested directory, with Folder derived from its path")
}

// TestFlows_Move_FolderOnly moves a workspace-tier flow between folders,
// keeping its tier and file name (PLAN §34f item 6): the counterpart to
// RescopeWith's tier-only move.
func TestFlows_Move_FolderOnly(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	ctx := context.Background()
	created, err := l.Flows().CreateIn(ctx, validFlowYAML, engine.CreateFlowOptions{OwnerKind: domain.FlowOwnerWorkspace})
	require.NoError(t, err)
	assert.Equal(t, "", created.Folder)

	moved, err := l.Flows().Move(ctx, created.ID, "promotions/2026")
	require.NoError(t, err)
	assert.Equal(t, "promotions/2026", moved.Folder)
	assert.Equal(t, domain.FlowOwnerWorkspace, moved.OwnerKind)
	assert.Equal(t, filepath.Base(created.Path), filepath.Base(moved.Path))
	assert.FileExists(t, filepath.Join(ws.Dir, domain.FlowsDir, "promotions", "2026", filepath.Base(created.Path)))
	assert.NoFileExists(t, created.Path)

	// Moving to the same folder is a no-op success.
	again, err := l.Flows().Move(ctx, created.ID, "promotions/2026")
	require.NoError(t, err)
	assert.Equal(t, moved.Path, again.Path)

	// Moving back to the root cleans up the now-empty "promotions/2026" and
	// "promotions" directories, but never the flows directory itself.
	back, err := l.Flows().Move(ctx, created.ID, "")
	require.NoError(t, err)
	assert.Equal(t, "", back.Folder)
	_, statErr := os.Stat(filepath.Join(ws.Dir, domain.FlowsDir, "promotions"))
	assert.True(t, os.IsNotExist(statErr), "expected the emptied promotions/ tree to be cleaned up")
	assert.DirExists(t, filepath.Join(ws.Dir, domain.FlowsDir))
}

// TestFlows_Move_ConflictAtDestination refuses to overwrite an existing
// file at the destination folder (PLAN §34f item 3).
func TestFlows_Move_ConflictAtDestination(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	ctx := context.Background()
	a, err := l.Flows().CreateIn(ctx, validFlowYAML, engine.CreateFlowOptions{OwnerKind: domain.FlowOwnerWorkspace})
	require.NoError(t, err)

	// A file already occupies the destination "taken/<a's file name>".
	require.NoError(t, os.MkdirAll(filepath.Join(ws.Dir, domain.FlowsDir, "taken"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws.Dir, domain.FlowsDir, "taken", filepath.Base(a.Path)), []byte("occupied"), 0o644))

	_, err = l.Flows().Move(ctx, a.ID, "taken")
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
}

// TestFlows_Create_DuplicateID_AcrossFolders_Conflicts: a flow id is unique
// across the whole tier regardless of folder (PLAN §34f item 2's "guard id
// uniqueness across folders").
func TestFlows_Create_DuplicateID_AcrossFolders_Conflicts(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	ctx := context.Background()
	_, err = l.Flows().CreateIn(ctx, validFlowYAML, engine.CreateFlowOptions{OwnerKind: domain.FlowOwnerWorkspace})
	require.NoError(t, err)

	// Same id ("order-allocation"), a different folder this time.
	_, err = l.Flows().CreateIn(ctx, validFlowYAML, engine.CreateFlowOptions{OwnerKind: domain.FlowOwnerWorkspace, Folder: "sub"})
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
}
