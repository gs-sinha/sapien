package example_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/example"
)

// TestStore_Create_WithFolder_WritesUnderFolder (PLAN §34f item 6): a
// caller-supplied Folder places the file in a subfolder of the scope's
// examples directory, and Folder round-trips on the created record.
func TestStore_Create_WithFolder_WritesUnderFolder(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t)

	ex := validExample("foldered")
	ex.Folder = "checkout/happy-path"
	got, err := s.Create(ctx, ex)
	require.NoError(t, err)
	assert.Equal(t, "checkout/happy-path", got.Folder)
	assert.Equal(t, filepath.Join(loc.WorkspaceDir, "examples", "checkout", "happy-path", "foldered.example.yaml"), got.Path)
	assert.FileExists(t, got.Path)

	reGet, err := s.Get(ctx, got.ID)
	require.NoError(t, err)
	assert.Equal(t, "checkout/happy-path", reGet.Folder)
}

func TestStore_Create_InvalidFolder_Rejected(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)

	ex := validExample("bad-folder")
	ex.Folder = "a/../b"
	_, err := s.Create(ctx, ex)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

// TestStore_Update_KeepsFolder (PLAN §34f item 6): editing content, or even
// trying to set a different Folder directly, never moves the file.
func TestStore_Update_KeepsFolder(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t)

	ex := validExample("original")
	ex.Folder = "a/b"
	created, err := s.Create(ctx, ex)
	require.NoError(t, err)
	originalPath := created.Path

	edited := *created
	edited.Description = "edited"
	edited.Folder = "somewhere/else" // an Update must not honor this
	got, err := s.Update(ctx, edited)
	require.NoError(t, err)
	assert.Equal(t, "a/b", got.Folder, "Update must keep the example's existing folder")
	assert.Equal(t, originalPath, got.Path)
	assert.FileExists(t, got.Path)
	assert.Equal(t, filepath.Join(loc.WorkspaceDir, "examples", "a", "b", "original.example.yaml"), got.Path)
}

// A tier move (Update with Tier set) keeps the example's folder, and cleans
// up the now-empty source folder (but never the tier root) once the file
// has left it (PLAN §34f item 2).
func TestStore_Update_TierMove_KeepsFolderAndCleansEmptyDir(t *testing.T) {
	ctx := context.Background()
	s, loc := newTierTestStore(t)

	ex := validExample("moving-with-folder")
	ex.Folder = "team/notes"
	created, err := s.Create(ctx, ex)
	require.NoError(t, err)
	require.Equal(t, domain.TierLocal, created.Tier)
	require.Equal(t, "team/notes", created.Folder)
	localFolderDir := filepath.Join(loc.LocalDir, "examples", "team", "notes")
	require.DirExists(t, localFolderDir)

	moved := *created
	moved.Tier = domain.TierWorkspace
	got, err := s.Update(ctx, moved)
	require.NoError(t, err)
	assert.Equal(t, domain.TierWorkspace, got.Tier)
	assert.Equal(t, "team/notes", got.Folder, "a tier move must keep the folder")
	assert.Equal(t, filepath.Join(loc.WorkspaceDir, "examples", "team", "notes", "moving-with-folder.example.yaml"), got.Path)
	assert.FileExists(t, got.Path)

	_, statErr := os.Stat(localFolderDir)
	assert.True(t, os.IsNotExist(statErr), "expected the emptied source folder to be removed")
	_, statErr = os.Stat(filepath.Join(loc.LocalDir, "examples", "team"))
	assert.True(t, os.IsNotExist(statErr), "expected the emptied parent folder to be removed too")
	assert.DirExists(t, filepath.Join(loc.LocalDir, "examples"), "the tier root itself must survive")
}

// TestStore_MoveFolder covers the dedicated folder-only move: basic move,
// no-op on the same folder, and refusing to overwrite an existing file at
// the destination (PLAN §34f item 3).
func TestStore_MoveFolder(t *testing.T) {
	ctx := context.Background()

	t.Run("moves the file and updates Folder", func(t *testing.T) {
		s, loc := newTestStore(t)
		created, err := s.Create(ctx, validExample("move-me"))
		require.NoError(t, err)
		require.Equal(t, "", created.Folder)

		moved, err := s.MoveFolder(ctx, created.ID, "a/b")
		require.NoError(t, err)
		assert.Equal(t, "a/b", moved.Folder)
		assert.Equal(t, filepath.Join(loc.WorkspaceDir, "examples", "a", "b", "move-me.example.yaml"), moved.Path)
		assert.FileExists(t, moved.Path)
		assert.NoFileExists(t, created.Path)
		assert.Equal(t, created.Tier, moved.Tier)
	})

	t.Run("root and empty string both mean root, and moving to the current folder is a no-op", func(t *testing.T) {
		s, _ := newTestStore(t)
		ex := validExample("already-here")
		ex.Folder = "x"
		created, err := s.Create(ctx, ex)
		require.NoError(t, err)

		again, err := s.MoveFolder(ctx, created.ID, "x")
		require.NoError(t, err)
		assert.Equal(t, created.Path, again.Path)

		toRoot, err := s.MoveFolder(ctx, created.ID, "/")
		require.NoError(t, err)
		assert.Equal(t, "", toRoot.Folder)
	})

	t.Run("refuses to overwrite an existing file at the destination", func(t *testing.T) {
		s, loc := newTestStore(t)
		a, err := s.Create(ctx, validExample("a-example"))
		require.NoError(t, err)

		destDir := filepath.Join(loc.WorkspaceDir, "examples", "taken")
		require.NoError(t, os.MkdirAll(destDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(destDir, "a-example.example.yaml"), []byte("occupied"), 0o644))

		_, err = s.MoveFolder(ctx, a.ID, "taken")
		require.Error(t, err)
		assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	})

	t.Run("refuses a read-only service", func(t *testing.T) {
		db := openTestDB(t)
		ws := t.TempDir()
		svcDir := t.TempDir()
		loc := example.Locator{
			WorkspaceDir: ws,
			ServiceDirs:  map[string]string{"order-service": svcDir},
		}
		s := example.New(db, loc)
		ex := validExample("svc-example")
		ex.Scope = domain.ExampleScopeService
		created, err := s.Create(ctx, ex)
		require.NoError(t, err)

		roLoc := loc
		roLoc.ReadOnly = map[string]bool{"order-service": true}
		roStore := example.New(db, roLoc)
		_, err = roStore.MoveFolder(ctx, created.ID, "sub")
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})
}

// TestStore_Reindex_FindsFolderedExample (PLAN §34f item 6): an example
// dropped by hand into a subfolder is indexed exactly like one at the root.
func TestStore_Reindex_FindsFolderedExample(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t)

	dir := filepath.Join(loc.WorkspaceDir, "examples", "a", "b")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	content := "version: 1\nid: nested-example\noperation: order-service.createOrder\nbody: { customerId: c1 }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nested-example.example.yaml"), []byte(content), 0o644))

	n, err := s.Reindex(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	got, err := s.Get(ctx, "nested-example")
	require.NoError(t, err)
	assert.Equal(t, "a/b", got.Folder)
}

// TestStore_List_FolderPrefixFilter checks the prefix semantics PLAN §34f
// item 4 specifies: "a" matches "a" and "a/b", not "ab".
func TestStore_List_FolderPrefixFilter(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)

	root, err := s.Create(ctx, validExample("root-ex"))
	require.NoError(t, err)
	exA := validExample("a-ex")
	exA.Folder = "a"
	a, err := s.Create(ctx, exA)
	require.NoError(t, err)
	exAB := validExample("ab-nested-ex")
	exAB.Folder = "a/b"
	ab, err := s.Create(ctx, exAB)
	require.NoError(t, err)
	exABNot := validExample("ab-sibling-ex")
	exABNot.Folder = "ab"
	abNot, err := s.Create(ctx, exABNot)
	require.NoError(t, err)

	got, err := s.List(ctx, domain.ExampleQuery{Folder: "a"})
	require.NoError(t, err)
	var ids []string
	for _, ex := range got {
		ids = append(ids, ex.ID)
	}
	assert.ElementsMatch(t, []string{a.ID, ab.ID}, ids)
	assert.NotContains(t, ids, root.ID)
	assert.NotContains(t, ids, abNot.ID)
}

// TestStore_List_TextSearch_FolderNameSurfacesItem (PLAN §34f item 7): a
// text query for the folder name finds examples in it.
func TestStore_List_TextSearch_FolderNameSurfacesItem(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)

	ex := validExample("qcomallocation-example")
	ex.Folder = "qcomallocation"
	created, err := s.Create(ctx, ex)
	require.NoError(t, err)

	got, err := s.List(ctx, domain.ExampleQuery{Text: "qcomallocation"})
	require.NoError(t, err)
	var found bool
	for _, r := range got {
		if r.ID == created.ID {
			found = true
		}
	}
	assert.True(t, found, "expected a text query for the folder name to surface the example in it")
}

// Two examples with the same id in different folders are still a conflict
// (PLAN §34f item 2: id uniqueness is global, independent of folder).
func TestStore_Create_DuplicateID_AcrossFolders_Conflicts(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)

	exA := validExample("dup-id")
	exA.Folder = "a"
	_, err := s.Create(ctx, exA)
	require.NoError(t, err)

	exB := validExample("dup-id")
	exB.Folder = "b"
	_, err = s.Create(ctx, exB)
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
}
