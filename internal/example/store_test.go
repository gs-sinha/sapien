package example_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/example"
)

func TestStore_Create_Defaults(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t)

	got, err := s.Create(ctx, domain.SavedExample{
		ID:        "create-qcom-order",
		Operation: "order-service.createOrder",
	})
	require.NoError(t, err)

	assert.Equal(t, 1, got.Version)
	assert.Equal(t, domain.ExampleScopeWorkspace, got.Scope)
	assert.Equal(t, "order-service", got.Service)
	assert.False(t, got.Created.IsZero())
	assert.Equal(t, got.Created, got.Updated)
	assert.Equal(t, filepath.Join(loc.WorkspaceDir, "examples", "create-qcom-order.example.yaml"), got.Path)

	data, err := os.ReadFile(got.Path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "operation: order-service.createOrder")
}

func TestStore_Create_ServiceScope(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t)

	t.Run("known service dir", func(t *testing.T) {
		got, err := s.Create(ctx, domain.SavedExample{
			ID:        "order-example",
			Operation: "order-service.createOrder",
			Scope:     domain.ExampleScopeService,
		})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(loc.ServiceDirs["order-service"], "examples", "order-example.example.yaml"), got.Path)
		_, err = os.Stat(got.Path)
		require.NoError(t, err)
	})

	t.Run("service scope without a known dir", func(t *testing.T) {
		_, err := s.Create(ctx, domain.SavedExample{
			ID:        "rider-example",
			Operation: "rider-service.getRider",
			Scope:     domain.ExampleScopeService,
		})
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})
}

func TestStore_Create_Validation(t *testing.T) {
	ctx := context.Background()

	idTests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"empty id rejected", "", true},
		{"uppercase rejected", "Create-Order", true},
		{"space rejected", "create order", true},
		{"dollar sign rejected", "create$order", true},
		{"lowercase with dash ok", "create-order", false},
		{"lowercase with underscore and dot ok", "create_order.v1", false},
	}
	for _, tt := range idTests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestStore(t)
			_, err := s.Create(ctx, domain.SavedExample{ID: tt.id, Operation: "order-service.createOrder"})
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, errs.Invalid, errs.CodeOf(err))
			} else {
				require.NoError(t, err)
			}
		})
	}

	opTests := []struct {
		name    string
		op      string
		wantErr bool
	}{
		{"empty operation rejected", "", true},
		{"no dot rejected", "createOrder", true},
		{"empty prefix rejected", ".createOrder", true},
		{"empty suffix rejected", "order-service.", true},
		{"well-formed accepted", "order-service.createOrder", false},
	}
	for _, tt := range opTests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestStore(t)
			_, err := s.Create(ctx, domain.SavedExample{ID: "an-example", Operation: tt.op})
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, errs.Invalid, errs.CodeOf(err))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestStore_Create_Conflict(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)

	_, err := s.Create(ctx, domain.SavedExample{ID: "dup", Operation: "order-service.createOrder"})
	require.NoError(t, err)

	_, err = s.Create(ctx, domain.SavedExample{ID: "dup", Operation: "order-service.createOrder"})
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
}

func TestStore_Get(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)

	created, err := s.Create(ctx, domain.SavedExample{
		ID:          "create-qcom-order",
		Operation:   "order-service.createOrder",
		Description: "QCOM order",
		Body:        map[string]any{"type": "QCOM"},
		Tags:        []string{"qcom"},
	})
	require.NoError(t, err)

	t.Run("found", func(t *testing.T) {
		got, err := s.Get(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, created.ID, got.ID)
		assert.Equal(t, created.Operation, got.Operation)
		assert.Equal(t, created.Description, got.Description)
		assert.Equal(t, created.Tags, got.Tags)
		assert.Equal(t, created.Scope, got.Scope)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := s.Get(ctx, "does-not-exist")
		require.Error(t, err)
		assert.Equal(t, errs.ExampleNotFound, errs.CodeOf(err))
		e := errs.As(err)
		assert.Equal(t, "does-not-exist", e.Details["id"])
	})
}

func TestStore_Update(t *testing.T) {
	ctx := context.Background()

	t.Run("rewrites file and bumps updated", func(t *testing.T) {
		s, _ := newTestStore(t)
		created, err := s.Create(ctx, domain.SavedExample{
			ID:        "create-qcom-order",
			Operation: "order-service.createOrder",
		})
		require.NoError(t, err)

		updated, err := s.Update(ctx, domain.SavedExample{
			ID:          created.ID,
			Operation:   created.Operation,
			Description: "now with a description",
		})
		require.NoError(t, err)
		assert.Equal(t, created.Created, updated.Created)
		assert.True(t, !updated.Updated.Before(created.Updated))
		assert.Equal(t, "now with a description", updated.Description)

		data, err := os.ReadFile(updated.Path)
		require.NoError(t, err)
		assert.Contains(t, string(data), "now with a description")
	})

	t.Run("scope move writes new file and removes old", func(t *testing.T) {
		s, loc := newTestStore(t)
		created, err := s.Create(ctx, domain.SavedExample{
			ID:        "movable",
			Operation: "order-service.createOrder",
			Scope:     domain.ExampleScopeWorkspace,
		})
		require.NoError(t, err)
		oldPath := created.Path
		_, err = os.Stat(oldPath)
		require.NoError(t, err)

		moved, err := s.Update(ctx, domain.SavedExample{
			ID:        created.ID,
			Operation: created.Operation,
			Scope:     domain.ExampleScopeService,
		})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(loc.ServiceDirs["order-service"], "examples", "movable.example.yaml"), moved.Path)

		_, err = os.Stat(oldPath)
		assert.True(t, os.IsNotExist(err), "old workspace file should be removed")
		_, err = os.Stat(moved.Path)
		require.NoError(t, err, "new service file should exist")
	})

	t.Run("requires an id", func(t *testing.T) {
		s, _ := newTestStore(t)
		_, err := s.Update(ctx, domain.SavedExample{Operation: "order-service.createOrder"})
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})

	t.Run("unknown id bubbles Get's not-found error", func(t *testing.T) {
		s, _ := newTestStore(t)
		_, err := s.Update(ctx, domain.SavedExample{ID: "nope", Operation: "order-service.createOrder"})
		require.Error(t, err)
		assert.Equal(t, errs.ExampleNotFound, errs.CodeOf(err))
	})

	t.Run("invalid operation rejected", func(t *testing.T) {
		s, _ := newTestStore(t)
		created, err := s.Create(ctx, domain.SavedExample{ID: "bad-op-update", Operation: "order-service.createOrder"})
		require.NoError(t, err)
		_, err = s.Update(ctx, domain.SavedExample{ID: created.ID, Operation: "no-dot-here"})
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})
}

func TestStore_Delete(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)

	created, err := s.Create(ctx, domain.SavedExample{ID: "to-delete", Operation: "order-service.createOrder"})
	require.NoError(t, err)

	require.NoError(t, s.Delete(ctx, created.ID))

	_, err = os.Stat(created.Path)
	assert.True(t, os.IsNotExist(err), "file should be removed")

	_, err = s.Get(ctx, created.ID)
	require.Error(t, err, "index row should be removed")

	t.Run("delete unknown id errors", func(t *testing.T) {
		err := s.Delete(ctx, "does-not-exist")
		require.Error(t, err)
		assert.Equal(t, errs.ExampleNotFound, errs.CodeOf(err))
	})
}

// writeRaw writes ex directly to disk at path (bypassing Store, so Created/
// Updated/Tags/etc. are exactly what the test controls) and returns path.
func writeRaw(t *testing.T, dir string, ex domain.SavedExample) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, example.FileName(ex.ID))
	data, err := example.Marshal(&ex)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
	return path
}

func TestStore_List(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t)

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	wsDir := filepath.Join(loc.WorkspaceDir, "examples")
	svcDir := filepath.Join(loc.ServiceDirs["order-service"], "examples")

	writeRaw(t, wsDir, domain.SavedExample{
		ID: "aaa-first", Operation: "order-service.createOrder", Description: "QCOM order in Bengaluru",
		Tags: []string{"qcom", "happy-path"}, Created: t0, Updated: t0,
	})
	writeRaw(t, wsDir, domain.SavedExample{
		ID: "bbb-second", Operation: "order-service.getOrder", Description: "fetch an order",
		Tags: []string{"happy"}, Created: t0.Add(5 * time.Second), Updated: t0.Add(5 * time.Second),
	})
	writeRaw(t, svcDir, domain.SavedExample{
		ID: "ccc-third", Operation: "order-service.cancelOrder", Description: "cancel flow",
		Tags: []string{"rider"}, Created: t0.Add(10 * time.Second), Updated: t0.Add(10 * time.Second),
	})
	// Same Updated as aaa-first, to exercise the "then id" tiebreaker.
	writeRaw(t, wsDir, domain.SavedExample{
		ID: "aab-tie", Operation: "order-service.createOrder", Description: "another one",
		Created: t0, Updated: t0,
	})

	n, err := s.Reindex(ctx)
	require.NoError(t, err)
	assert.Equal(t, 4, n)

	t.Run("no filter, ordered by updated desc then id asc", func(t *testing.T) {
		got, err := s.List(ctx, domain.ExampleQuery{})
		require.NoError(t, err)
		require.Len(t, got, 4)
		ids := []string{got[0].ID, got[1].ID, got[2].ID, got[3].ID}
		assert.Equal(t, []string{"ccc-third", "bbb-second", "aaa-first", "aab-tie"}, ids)
	})

	t.Run("filter by operation exact", func(t *testing.T) {
		got, err := s.List(ctx, domain.ExampleQuery{Operation: "order-service.getOrder"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "bbb-second", got[0].ID)
	})

	t.Run("filter by service exact", func(t *testing.T) {
		got, err := s.List(ctx, domain.ExampleQuery{Service: "order-service"})
		require.NoError(t, err)
		assert.Len(t, got, 4)
	})

	t.Run("filter by tag exact, no substring cross-match", func(t *testing.T) {
		got, err := s.List(ctx, domain.ExampleQuery{Tag: "happy"})
		require.NoError(t, err)
		require.Len(t, got, 1, "must not match tag 'happy-path' as a substring of 'happy'")
		assert.Equal(t, "bbb-second", got[0].ID)

		got, err = s.List(ctx, domain.ExampleQuery{Tag: "happy-path"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "aaa-first", got[0].ID)
	})

	t.Run("filter by text, case-insensitive, over id/description/tags", func(t *testing.T) {
		got, err := s.List(ctx, domain.ExampleQuery{Text: "bengaluru"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "aaa-first", got[0].ID)

		got, err = s.List(ctx, domain.ExampleQuery{Text: "RIDER"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "ccc-third", got[0].ID)

		got, err = s.List(ctx, domain.ExampleQuery{Text: "aaa"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "aaa-first", got[0].ID)
	})

	t.Run("limit", func(t *testing.T) {
		got, err := s.List(ctx, domain.ExampleQuery{Limit: 2})
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("no match returns empty, not error", func(t *testing.T) {
		got, err := s.List(ctx, domain.ExampleQuery{Operation: "no-such.op"})
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestStore_ForOperations(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t)
	wsDir := filepath.Join(loc.WorkspaceDir, "examples")
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	writeRaw(t, wsDir, domain.SavedExample{
		ID: "unverified-newer", Operation: "order-service.createOrder",
		Created: t0, Updated: t0.Add(20 * time.Second),
	})
	writeRaw(t, wsDir, domain.SavedExample{
		ID: "verified-older", Operation: "order-service.createOrder",
		Created: t0, Updated: t0.Add(5 * time.Second),
		Verified: &domain.ExampleVerified{Env: "stage", At: t0},
	})
	writeRaw(t, wsDir, domain.SavedExample{
		ID: "other-op", Operation: "order-service.getOrder",
		Created: t0, Updated: t0.Add(30 * time.Second),
	})

	_, err := s.Reindex(ctx)
	require.NoError(t, err)

	t.Run("verified first, then updated desc", func(t *testing.T) {
		got, err := s.ForOperations(ctx, []string{"order-service.createOrder"}, 10)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "verified-older", got[0].ID, "verified must sort first even though it is older")
		assert.Equal(t, "unverified-newer", got[1].ID)
	})

	t.Run("multiple operations", func(t *testing.T) {
		got, err := s.ForOperations(ctx, []string{"order-service.createOrder", "order-service.getOrder"}, 10)
		require.NoError(t, err)
		assert.Len(t, got, 3)
	})

	t.Run("limit applies", func(t *testing.T) {
		got, err := s.ForOperations(ctx, []string{"order-service.createOrder", "order-service.getOrder"}, 1)
		require.NoError(t, err)
		assert.Len(t, got, 1)
	})

	t.Run("empty operation list returns nil", func(t *testing.T) {
		got, err := s.ForOperations(ctx, nil, 10)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("no matching operation returns empty", func(t *testing.T) {
		got, err := s.ForOperations(ctx, []string{"no-such.op"}, 10)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestStore_Reindex(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t)

	// A workspace example created normally.
	ws, err := s.Create(ctx, domain.SavedExample{ID: "ws-example", Operation: "order-service.createOrder"})
	require.NoError(t, err)

	// A service example created normally.
	svc, err := s.Create(ctx, domain.SavedExample{
		ID: "svc-example", Operation: "order-service.getOrder", Scope: domain.ExampleScopeService,
	})
	require.NoError(t, err)

	t.Run("reindex with nothing changed re-indexes existing files", func(t *testing.T) {
		n, err := s.Reindex(ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, n)
	})

	t.Run("a file added by hand appears after reindex", func(t *testing.T) {
		writeRaw(t, filepath.Join(loc.WorkspaceDir, "examples"), domain.SavedExample{
			ID: "hand-added", Operation: "order-service.createOrder",
			Created: time.Now().UTC(), Updated: time.Now().UTC(),
		})

		n, err := s.Reindex(ctx)
		require.NoError(t, err)
		assert.Equal(t, 3, n)

		got, err := s.Get(ctx, "hand-added")
		require.NoError(t, err)
		assert.Equal(t, "order-service.createOrder", got.Operation)
	})

	t.Run("deleting a file removes its row on reindex", func(t *testing.T) {
		require.NoError(t, os.Remove(ws.Path))

		n, err := s.Reindex(ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, n) // svc-example + hand-added remain

		_, err = s.Get(ctx, ws.ID)
		require.Error(t, err, "expected the row for the deleted file to be gone")

		_, err = s.Get(ctx, svc.ID)
		require.NoError(t, err, "service example must survive reindex")
	})

	t.Run("a malformed file is skipped and reported, not fatal", func(t *testing.T) {
		badPath := filepath.Join(loc.WorkspaceDir, "examples", "broken.example.yaml")
		require.NoError(t, os.WriteFile(badPath, []byte("{ not: valid: yaml"), 0o644))

		n, err := s.Reindex(ctx)
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
		e := errs.As(err)
		files, ok := e.Details["files"].([]string)
		require.True(t, ok)
		assert.Contains(t, files, badPath)
		assert.Equal(t, 2, n, "good files still get indexed despite the bad one")
	})
}

func TestStore_IndexOneAndRemovePath(t *testing.T) {
	ctx := context.Background()
	s, loc := newTestStore(t)

	t.Run("IndexOne on a workspace path", func(t *testing.T) {
		path := writeRaw(t, filepath.Join(loc.WorkspaceDir, "examples"), domain.SavedExample{
			ID: "watcher-added", Operation: "order-service.createOrder",
			Created: time.Now().UTC(), Updated: time.Now().UTC(),
		})

		require.NoError(t, s.IndexOne(ctx, path))
		got, err := s.Get(ctx, "watcher-added")
		require.NoError(t, err)
		assert.Equal(t, domain.ExampleScopeWorkspace, got.Scope)
		assert.Equal(t, path, got.Path)

		require.NoError(t, s.RemovePath(ctx, path))
		_, err = s.Get(ctx, "watcher-added")
		require.Error(t, err)
	})

	t.Run("IndexOne on a service path infers service scope", func(t *testing.T) {
		path := writeRaw(t, filepath.Join(loc.ServiceDirs["order-service"], "examples"), domain.SavedExample{
			ID: "watcher-added-svc", Operation: "order-service.createOrder",
			Created: time.Now().UTC(), Updated: time.Now().UTC(),
		})

		require.NoError(t, s.IndexOne(ctx, path))
		got, err := s.Get(ctx, "watcher-added-svc")
		require.NoError(t, err)
		assert.Equal(t, domain.ExampleScopeService, got.Scope)
	})

	t.Run("RemovePath on an unknown path is a no-op", func(t *testing.T) {
		require.NoError(t, s.RemovePath(ctx, filepath.Join(loc.WorkspaceDir, "examples", "does-not-exist.example.yaml")))
	})
}
