package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/store"
)

// expectedTables are every table PLAN.md §15 names, adapted to valid SQLite
// names, plus the trigram substring index and the migrations ledger.
var expectedTables = []string{
	"services",
	"contract_files",
	"operations",
	"operation_aliases",
	"schemas",
	"fields",
	"operations_fts",
	"operations_trigram",
	"docs",
	"doc_sections",
	"doc_refs",
	"docs_fts",
	"flows",
	"memories",
	"memory_subjects",
	"memories_fts",
	"runs",
	"run_steps",
	"environments",
	"settings",
	"schema_migrations",
}

func openTestDB(t *testing.T, opts ...store.Option) *store.DB {
	t.Helper()
	db, err := store.Open(":memory:", opts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpen_MemoryMigratesAndCreatesTables(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	names := tableNames(t, db)
	for _, want := range expectedTables {
		assert.Containsf(t, names, want, "expected table %q to exist", want)
	}

	v, err := db.Version(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, v, 1, "at least the init migration must be applied")
}

func TestOpen_FileCreatesParentDirAndMigrates(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "sapien.db")

	db, err := store.Open(dbPath)
	require.NoError(t, err)
	defer db.Close()

	names := tableNames(t, db)
	for _, want := range expectedTables {
		assert.Containsf(t, names, want, "expected table %q to exist", want)
	}

	_, err = filepath.Glob(dbPath)
	require.NoError(t, err)
}

func TestOpen_FileUsesWALMode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sapien.db")

	db, err := store.Open(dbPath)
	require.NoError(t, err)
	defer db.Close()

	var mode string
	require.NoError(t, db.SQL().QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode))
	assert.Equal(t, "wal", mode)
}

func TestOpen_PragmasApplied(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	tests := []struct {
		pragma string
		want   int
	}{
		{"foreign_keys", 1},
		{"synchronous", 1}, // NORMAL
		{"busy_timeout", 5000},
		{"temp_store", 2}, // MEMORY
	}

	for _, tt := range tests {
		t.Run(tt.pragma, func(t *testing.T) {
			var got int
			require.NoError(t, db.SQL().QueryRowContext(ctx, "PRAGMA "+tt.pragma).Scan(&got))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMigrate_IdempotentOnFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sapien.db")
	ctx := context.Background()

	db, err := store.Open(dbPath)
	require.NoError(t, err)
	v1, err := db.Version(ctx)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// Reopen: must not error and must not reapply anything.
	db2, err := store.Open(dbPath)
	require.NoError(t, err)
	defer db2.Close()
	v2, err := db2.Version(ctx)
	require.NoError(t, err)
	assert.Equal(t, v1, v2)

	var count int
	require.NoError(t, db2.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count))
	assert.Equal(t, v2, count, "one schema_migrations row per applied version, no duplicates")

	// Calling Migrate directly again is also a no-op.
	require.NoError(t, db2.Migrate(ctx))
	var count2 int
	require.NoError(t, db2.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count2))
	assert.Equal(t, count, count2)
}

func TestMigrate_WithoutMigrationOption(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sapien.db")
	ctx := context.Background()

	db, err := store.Open(dbPath, store.WithoutMigration())
	require.NoError(t, err)
	defer db.Close()

	// No tables beyond sqlite internals should exist yet.
	names := tableNames(t, db)
	assert.NotContains(t, names, "services")

	require.NoError(t, db.Migrate(ctx))
	names = tableNames(t, db)
	assert.Contains(t, names, "services")

	v, err := db.Version(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, v, 1, "at least the init migration must be applied")
}

func TestDB_WriteCommitsAndRollsBack(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('a', '1')`)
		return err
	})
	require.NoError(t, err)

	var value string
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'a'`).Scan(&value))
	assert.Equal(t, "1", value)

	sentinel := errors.New("boom")
	err = db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('b', '2')`); err != nil {
			return err
		}
		return sentinel
	})
	assert.ErrorIs(t, err, sentinel)

	var count int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key = 'b'`).Scan(&count))
	assert.Equal(t, 0, count, "rolled-back write must not be visible")
}

func TestDB_Read(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	require.NoError(t, db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('k', 'v')`)
		return err
	}))

	err := db.Read(ctx, func(conn *sql.Conn) error {
		var v string
		if err := conn.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'k'`).Scan(&v); err != nil {
			return err
		}
		assert.Equal(t, "v", v)
		return nil
	})
	require.NoError(t, err)
}

func TestDB_Path(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sapien.db")
	db, err := store.Open(dbPath)
	require.NoError(t, err)
	defer db.Close()
	assert.Equal(t, dbPath, db.Path())
}

func TestOpen_WithMaxOpenConns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sapien.db")
	db, err := store.Open(dbPath, store.WithMaxOpenConns(3))
	require.NoError(t, err)
	defer db.Close()

	assert.Equal(t, 3, db.SQL().Stats().MaxOpenConnections)
}

func TestOpen_WithMaxOpenConnsZeroFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sapien.db")
	db, err := store.Open(dbPath, store.WithMaxOpenConns(0))
	require.NoError(t, err)
	defer db.Close()

	assert.Equal(t, 8, db.SQL().Stats().MaxOpenConnections)
}

func TestOpen_RejectsEmptyPath(t *testing.T) {
	_, err := store.Open("")
	assert.Error(t, err)
}

func tableNames(t *testing.T, db *store.DB) []string {
	t.Helper()
	rows, err := db.SQL().QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	require.NoError(t, err)
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		names = append(names, n)
	}
	require.NoError(t, rows.Err())
	return names
}
