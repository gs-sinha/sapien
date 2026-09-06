package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/store"
)

func TestDB_WriteSerializesConcurrentWriters(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = db.Write(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx,
					`INSERT INTO settings (key, value) VALUES (?, ?)`,
					fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i))
				return err
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "writer %d failed (SQLITE_BUSY would surface here)", i)
	}

	var count int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM settings`).Scan(&count))
	assert.Equal(t, n, count)
}

func TestDB_FileWriteSerializesConcurrentWriters(t *testing.T) {
	// Same as above but against an on-disk WAL database, where SQLITE_BUSY is
	// the real failure mode Write is meant to prevent.
	dir := t.TempDir()
	db, err := store.Open(dir + "/sapien.db")
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = db.Write(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx,
					`INSERT INTO settings (key, value) VALUES (?, ?)`,
					fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i))
				return err
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "writer %d failed (SQLITE_BUSY would surface here)", i)
	}

	var count int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM settings`).Scan(&count))
	assert.Equal(t, n, count)
}

func TestNewID_MonotonicAndUniqueAcrossConcurrentCalls(t *testing.T) {
	const n = 10000
	ids := make([]string, n)

	var wg sync.WaitGroup
	const workers = 20 // n (10000) divides evenly by workers, so every index is covered
	perWorker := n / workers
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				idx := w*perWorker + i
				ids[idx] = store.NewID("mem")
			}
		}(w)
	}
	wg.Wait()

	seen := make(map[string]struct{}, n)
	for _, id := range ids {
		require.Regexp(t, `^mem_[0-9A-Z]{26}$`, id)
		_, dup := seen[id]
		require.Falsef(t, dup, "duplicate id generated: %s", id)
		seen[id] = struct{}{}
	}
	assert.Len(t, seen, n)
}

func TestNewID_MonotonicSequential(t *testing.T) {
	const n = 10000
	prev := store.NewID("run")
	for i := 0; i < n; i++ {
		next := store.NewID("run")
		assert.Less(t, prev, next, "sequential IDs generated in the same process must sort strictly increasing")
		prev = next
	}
}

func TestNewID_DifferentPrefixes(t *testing.T) {
	memID := store.NewID("mem")
	runID := store.NewID("run")
	assert.Regexp(t, `^mem_`, memID)
	assert.Regexp(t, `^run_`, runID)
}
