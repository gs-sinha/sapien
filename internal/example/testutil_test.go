package example_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/example"
	"github.com/gs-sinha/sapien/internal/store"
)

// openTestDB opens an in-memory SQLite database (migrated), closing it on
// test cleanup.
func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newTestStore builds a Store wired to a temp workspace dir and a temp
// "order-service" package dir, over a fresh in-memory DB.
func newTestStore(t *testing.T) (*example.Store, example.Locator) {
	t.Helper()
	db := openTestDB(t)
	ws := t.TempDir()
	svcDir := t.TempDir()
	loc := example.Locator{
		WorkspaceDir: ws,
		ServiceDirs:  map[string]string{"order-service": svcDir},
	}
	return example.New(db, loc), loc
}
