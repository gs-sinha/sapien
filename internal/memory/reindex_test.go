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
	"github.com/gs-sinha/sapien/internal/memory"
)

func TestStore_Reindex(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t, nil)

	// A workspace memory created normally.
	ws, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "workspace memory"})
	require.NoError(t, err)

	// A personal memory: never touched by Reindex.
	personal, err := s.Create(ctx, domain.Memory{Scope: domain.ScopePersonal, Text: "personal memory"})
	require.NoError(t, err)

	t.Run("reindex with nothing changed re-indexes existing files", func(t *testing.T) {
		n, err := s.Reindex(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, n) // only the one workspace file

		_, err = s.Get(ctx, ws.ID)
		require.NoError(t, err)
		_, err = s.Get(ctx, personal.ID)
		require.NoError(t, err, "personal memory must survive reindex")
	})

	t.Run("a file added by hand appears after reindex", func(t *testing.T) {
		m := domain.Memory{
			ID:      "mem_01J8Z5K3W2RQ4X7M9NPQR2T3ZZ",
			Scope:   domain.ScopeWorkspace,
			Created: time.Now().UTC(),
			Updated: time.Now().UTC(),
			Text:    "added by hand",
		}
		path := filepath.Join(loc.WorkspaceDir, "memories", memory.FileName(m.ID))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, memory.Format(m), 0o644))

		n, err := s.Reindex(ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, n) // the original ws file + the hand-added one

		got, err := s.Get(ctx, m.ID)
		require.NoError(t, err)
		assert.Equal(t, "added by hand", got.Text)
	})

	t.Run("deleting a file removes its row on reindex", func(t *testing.T) {
		require.NoError(t, os.Remove(ws.FilePath))

		n, err := s.Reindex(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, n) // only the hand-added file remains

		_, err = s.Get(ctx, ws.ID)
		require.Error(t, err, "expected the row for the deleted file to be gone")

		_, err = s.Get(ctx, personal.ID)
		require.NoError(t, err, "personal memory must still survive reindex")
	})
}

func TestStore_IndexOneAndRemovePath(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t, nil)

	m := domain.Memory{
		ID:      "mem_01J8Z5K3W2RQ4X7M9NPQR2T3ZY",
		Scope:   domain.ScopeWorkspace,
		Created: time.Now().UTC(),
		Updated: time.Now().UTC(),
		Text:    "watcher-added memory",
	}
	path := filepath.Join(loc.WorkspaceDir, "memories", memory.FileName(m.ID))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, memory.Format(m), 0o644))

	t.Run("IndexOne indexes a file not yet known to the store", func(t *testing.T) {
		require.NoError(t, s.IndexOne(ctx, path))
		got, err := s.Get(ctx, m.ID)
		require.NoError(t, err)
		assert.Equal(t, "watcher-added memory", got.Text)
		assert.Equal(t, path, got.FilePath)
	})

	t.Run("RemovePath removes the indexed row for that path", func(t *testing.T) {
		require.NoError(t, s.RemovePath(ctx, path))
		_, err := s.Get(ctx, m.ID)
		require.Error(t, err)
	})

	t.Run("RemovePath on an unknown path is a no-op", func(t *testing.T) {
		require.NoError(t, s.RemovePath(ctx, filepath.Join(loc.WorkspaceDir, "memories", "does-not-exist.md")))
	})
}
