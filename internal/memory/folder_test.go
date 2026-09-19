package memory_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/memory"
)

// TestStore_Create_WithFolder_WritesUnderFolder (PLAN §34f item 6): a
// caller-supplied Folder places the file in a subfolder of the scope's
// memories directory, and Folder round-trips on the created record.
func TestStore_Create_WithFolder_WritesUnderFolder(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t, nil)

	got, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "foldered", Folder: "incidents/2026-09"})
	require.NoError(t, err)
	assert.Equal(t, "incidents/2026-09", got.Folder)
	assert.Equal(t, filepath.Join(loc.WorkspaceDir, "memories", "incidents", "2026-09", memory.FileName(got.ID)), got.FilePath)
	assert.FileExists(t, got.FilePath)

	reGet, err := s.Get(ctx, got.ID)
	require.NoError(t, err)
	assert.Equal(t, "incidents/2026-09", reGet.Folder)
}

// An invalid folder (PLAN §34f item 1's rules) is rejected before anything
// is written.
func TestStore_Create_InvalidFolder_Rejected(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	_, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "bad folder", Folder: "../escape"})
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// TestStore_Update_KeepsFolder (PLAN §34f item 6: "an update rewrites the
// file IN PLACE, keeps its folder"): editing text, or even trying to set a
// different Folder directly on the call, never moves the file.
func TestStore_Update_KeepsFolder(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t, nil)

	created, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "original", Folder: "a/b"})
	require.NoError(t, err)
	originalPath := created.FilePath

	edited := *created
	edited.Text = "edited"
	edited.Folder = "somewhere/else" // an Update must not honor this
	got, err := s.Update(ctx, edited)
	require.NoError(t, err)
	assert.Equal(t, "a/b", got.Folder, "Update must keep the memory's existing folder")
	assert.Equal(t, originalPath, got.FilePath)
	assert.FileExists(t, got.FilePath)
	assert.Equal(t, filepath.Join(loc.WorkspaceDir, "memories", "a", "b", memory.FileName(created.ID)), got.FilePath)
}

// A tier move (Update with Tier set) keeps the memory's folder, and cleans
// up the now-empty source folder (but never the tier root) once the file
// has left it (PLAN §34f item 2).
func TestStore_Update_TierMove_KeepsFolderAndCleansEmptyDir(t *testing.T) {
	ctx := context.Background()
	s, loc := newTierTestStore(t)

	created, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "moving with folder", Folder: "team/notes"})
	require.NoError(t, err)
	require.Equal(t, domain.TierLocal, created.Tier)
	require.Equal(t, "team/notes", created.Folder)
	localFolderDir := filepath.Join(loc.LocalDir, "memories", "team", "notes")
	require.DirExists(t, localFolderDir)

	moved := *created
	moved.Tier = domain.TierWorkspace
	got, err := s.Update(ctx, moved)
	require.NoError(t, err)
	assert.Equal(t, domain.TierWorkspace, got.Tier)
	assert.Equal(t, "team/notes", got.Folder, "a tier move must keep the folder")
	assert.Equal(t, filepath.Join(loc.WorkspaceDir, "memories", "team", "notes", memory.FileName(created.ID)), got.FilePath)
	assert.FileExists(t, got.FilePath)

	// The emptied local/memories/team/notes (and team/) tree is cleaned up,
	// but local/memories itself (the tier root) survives.
	_, statErr := os.Stat(localFolderDir)
	assert.True(t, os.IsNotExist(statErr), "expected the emptied source folder to be removed")
	_, statErr = os.Stat(filepath.Join(loc.LocalDir, "memories", "team"))
	assert.True(t, os.IsNotExist(statErr), "expected the emptied parent folder to be removed too")
	assert.DirExists(t, filepath.Join(loc.LocalDir, "memories"), "the tier root itself must survive")
}

// TestStore_MoveFolder covers the dedicated folder-only move: basic move,
// no-op on the same folder, and refusing to overwrite an existing file at
// the destination (PLAN §34f item 3).
func TestStore_MoveFolder(t *testing.T) {
	ctx := context.Background()

	t.Run("moves the file and updates Folder", func(t *testing.T) {
		s, loc := newTestStore(t, nil)
		created, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "move me"})
		require.NoError(t, err)
		require.Equal(t, "", created.Folder)

		moved, err := s.MoveFolder(ctx, created.ID, "a/b")
		require.NoError(t, err)
		assert.Equal(t, "a/b", moved.Folder)
		assert.Equal(t, filepath.Join(loc.WorkspaceDir, "memories", "a", "b", memory.FileName(created.ID)), moved.FilePath)
		assert.FileExists(t, moved.FilePath)
		assert.NoFileExists(t, created.FilePath)

		// Tier is unchanged by a folder move.
		assert.Equal(t, created.Tier, moved.Tier)
	})

	t.Run("root and empty string both mean root, and moving to the current folder is a no-op", func(t *testing.T) {
		s, _ := newTestStore(t, nil)
		created, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "already here", Folder: "x"})
		require.NoError(t, err)

		again, err := s.MoveFolder(ctx, created.ID, "x")
		require.NoError(t, err)
		assert.Equal(t, created.FilePath, again.FilePath)

		toRoot, err := s.MoveFolder(ctx, created.ID, "/")
		require.NoError(t, err)
		assert.Equal(t, "", toRoot.Folder)
	})

	t.Run("refuses to overwrite an existing file at the destination", func(t *testing.T) {
		s, loc := newTestStore(t, nil)
		a, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "a"})
		require.NoError(t, err)

		// Manufacture a collision: a file already sitting where a's move
		// would land.
		destDir := filepath.Join(loc.WorkspaceDir, "memories", "taken")
		require.NoError(t, os.MkdirAll(destDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(destDir, memory.FileName(a.ID)), []byte("occupied"), 0o644))

		_, err = s.MoveFolder(ctx, a.ID, "taken")
		require.Error(t, err)
		assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	})

	t.Run("personal scope has no file to move", func(t *testing.T) {
		s, _ := newTestStore(t, nil)
		created, err := s.Create(ctx, domain.Memory{Scope: domain.ScopePersonal, Text: "personal"})
		require.NoError(t, err)

		_, err = s.MoveFolder(ctx, created.ID, "somewhere")
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})

	t.Run("refuses a read-only service", func(t *testing.T) {
		db := openTestDB(t)
		ws := t.TempDir()
		svcDir := t.TempDir()
		loc := memory.Locator{
			WorkspaceDir: ws,
			ServiceDirs:  map[string]string{"rider-service": svcDir},
		}
		s := memory.New(db, loc, nil)
		created, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeService, Subject: domain.Subject{Service: "rider-service"}, Text: "svc"})
		require.NoError(t, err)

		roLoc := loc
		roLoc.ReadOnly = memory.ReadOnlyServices{"rider-service": true}
		roStore := memory.New(db, roLoc, nil)
		_, err = roStore.MoveFolder(ctx, created.ID, "sub")
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})
}

// TestStore_Reindex_FindsFolderedMemory (PLAN §34f item 6): a memory
// dropped by hand into a subfolder is indexed exactly like one at the root.
func TestStore_Reindex_FindsFolderedMemory(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t, nil)

	m := domain.Memory{
		ID:      "mem_01J8Z5K3W2RQ4X7M9NPQR2T3ZZ",
		Scope:   domain.ScopeWorkspace,
		Created: time.Now().UTC(),
		Updated: time.Now().UTC(),
		Text:    "added by hand, nested",
	}
	dir := filepath.Join(loc.WorkspaceDir, "memories", "a", "b")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, memory.FileName(m.ID)), memory.Format(m), 0o644))

	n, err := s.Reindex(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	got, err := s.Get(ctx, m.ID)
	require.NoError(t, err)
	assert.Equal(t, "a/b", got.Folder)
}

// TestStore_List_FolderPrefixFilter checks the prefix semantics PLAN §34f
// item 4 specifies: "a" matches "a" and "a/b", not "ab".
func TestStore_List_FolderPrefixFilter(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	root, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "root"})
	require.NoError(t, err)
	a, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "in a", Folder: "a"})
	require.NoError(t, err)
	ab, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "in a/b", Folder: "a/b"})
	require.NoError(t, err)
	abNot, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "in ab", Folder: "ab"})
	require.NoError(t, err)

	got, err := s.List(ctx, domain.MemoryQuery{Folder: "a"})
	require.NoError(t, err)
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	assert.ElementsMatch(t, []string{a.ID, ab.ID}, ids)
	assert.NotContains(t, ids, root.ID)
	assert.NotContains(t, ids, abNot.ID)
}

// TestStore_Search_FolderNameSurfacesItem (PLAN §34f item 7): a lexical
// query for the folder name finds memories in it, the same way a query for
// a tag or subject value would.
func TestStore_Search_FolderNameSurfacesItem(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	created, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "nothing text-matching here", Folder: "qcomallocation"})
	require.NoError(t, err)

	results, err := s.Search(ctx, domain.MemoryQuery{Text: "qcomallocation"})
	require.NoError(t, err)
	var found bool
	for _, r := range results {
		if r.Memory.ID == created.ID {
			found = true
		}
	}
	assert.True(t, found, "expected a query for the folder name to surface the memory in it")
}
