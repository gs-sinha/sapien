package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/store"
)

// applyMigrationsThrough manually replays every embedded migration file
// (read straight from the migrations/ directory next to this test, the same
// files store's own go:embed serves) whose numeric prefix is less than
// before, against db, recording each in schema_migrations exactly as
// store.DB.Migrate would -- so a later store.Open(path) on the same file
// sees them as already applied and only runs what's left. Used to build a
// database that predates a specific migration, for testing that migration's
// effect on a database that already has rows in the table it changes.
func applyMigrationsThrough(t *testing.T, db *sql.DB, before string) {
	t.Helper()
	ctx := context.Background()

	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`)
	require.NoError(t, err)

	entries, err := os.ReadDir("migrations")
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		if name >= before {
			break
		}
		script, err := os.ReadFile(filepath.Join("migrations", name))
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(script))
		require.NoError(t, err, "applying %s", name)

		version := name[:3]
		_, err = db.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			mustAtoi(t, version), name, time.Now().UTC().Format(time.RFC3339))
		require.NoError(t, err)
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		require.True(t, c >= '0' && c <= '9', "not a digit: %q", s)
		n = n*10 + int(c-'0')
	}
	return n
}

// TestMigrate_LoopSteps_OnPre009DatabaseWithRows confirms 009_loop_steps.sql
// (PLAN §34f.8) applies cleanly to a database created by the pre-009
// schema that already has run_steps rows in it, and that those rows survive
// with iteration/parent/kind/count defaulted (-1/""/""/0) rather than being
// lost or corrupted by the table rebuild the migration performs (SQLite has
// no ALTER TABLE ... CHANGE PRIMARY KEY).
func TestMigrate_LoopSteps_OnPre009DatabaseWithRows(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sapien.db")
	ctx := context.Background()

	db, err := store.Open(dbPath, store.WithoutMigration())
	require.NoError(t, err)
	applyMigrationsThrough(t, db.SQL(), "009_")

	_, err = db.SQL().ExecContext(ctx, `INSERT INTO runs (id, status, started) VALUES ('run_old', 'passed', '2026-01-01T00:00:00Z')`)
	require.NoError(t, err)
	_, err = db.SQL().ExecContext(ctx,
		`INSERT INTO run_steps (run_id, step_id, idx, status, skip_reason, operation, attempts) VALUES ('run_old', 'a', 0, 'passed', '', 'svc.op', 1)`)
	require.NoError(t, err)
	_, err = db.SQL().ExecContext(ctx,
		`INSERT INTO run_steps (run_id, step_id, idx, status, skip_reason, operation, attempts) VALUES ('run_old', 'b', 1, 'failed', 'when', 'svc.op2', 0)`)
	require.NoError(t, err)

	require.NoError(t, db.Close())

	// Reopen normally: Migrate() applies 009 (and only 009 -- everything
	// else is already recorded as applied) against a database that already
	// has rows in the table it rebuilds.
	db2, err := store.Open(dbPath)
	require.NoError(t, err)
	defer db2.Close()

	v, err := db2.Version(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, v, 9)

	rows, err := db2.SQL().QueryContext(ctx,
		`SELECT step_id, idx, status, skip_reason, operation, iteration, parent, kind, count FROM run_steps WHERE run_id = 'run_old' ORDER BY idx`)
	require.NoError(t, err)
	defer rows.Close()

	type row struct {
		stepID, status, skipReason, operation, parent, kind string
		idx, iteration, count                               int
	}
	var got []row
	for rows.Next() {
		var r row
		require.NoError(t, rows.Scan(&r.stepID, &r.idx, &r.status, &r.skipReason, &r.operation, &r.iteration, &r.parent, &r.kind, &r.count))
		got = append(got, r)
	}
	require.NoError(t, rows.Err())
	require.Len(t, got, 2)

	assert.Equal(t, "a", got[0].stepID)
	assert.Equal(t, "passed", got[0].status)
	assert.Equal(t, "svc.op", got[0].operation)
	assert.Equal(t, -1, got[0].iteration, "a pre-009 row must default to iteration -1 (not in a loop)")
	assert.Equal(t, "", got[0].parent)
	assert.Equal(t, "", got[0].kind)
	assert.Equal(t, 0, got[0].count)

	assert.Equal(t, "b", got[1].stepID)
	assert.Equal(t, "when", got[1].skipReason, "a column added by an earlier migration (008) must also survive")
	assert.Equal(t, -1, got[1].iteration)
}
