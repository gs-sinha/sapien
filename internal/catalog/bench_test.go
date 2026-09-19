package catalog_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/catalog"
	"github.com/gs-sinha/sapien/internal/domain"
)

func TestApply_Large1000Operations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large Apply benchmark in -short mode")
	}

	const n = 1000
	svc := domain.Service{ID: "bench-service", Name: "bench-service"}

	ops := make([]domain.Operation, 0, n)
	fields := make([]domain.Field, 0, n*2)
	aliases := make([]domain.Alias, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("bench-service.op%04d", i)
		path := fmt.Sprintf("/v1/things/%d/{itemId}", i)
		ops = append(ops, domain.Operation{
			ID: id, ServiceID: svc.ID, Protocol: domain.ProtocolHTTP,
			HTTP:    &domain.HTTPBinding{Method: "GET", Path: path},
			RawOpID: fmt.Sprintf("op%04d", i),
			Summary: fmt.Sprintf("Operation number %d", i),
			Tags:    []string{"bench"},
			Params:  []domain.Param{{Name: "itemId", In: domain.InPath, Required: true}},
			Hash:    fmt.Sprintf("hash-%d-v1", i),
		})
		fields = append(fields,
			domain.Field{OperationID: id, Path: "request.path.itemId", Type: "string"},
			domain.Field{OperationID: id, Path: "response.200.body.itemId", Type: "string"},
		)
		aliases = append(aliases, domain.Alias{Method: "GET", Path: path, OperationID: id})
	}

	snap := catalog.Snapshot{Service: svc, Operations: ops, Fields: fields, Aliases: aliases}

	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()

	start := time.Now()
	change, err := c.Apply(ctx, snap)
	elapsed := time.Since(start)
	require.NoError(t, err)
	assert.Len(t, change.Added, n)
	limit := 3 * time.Second
	if raceEnabled {
		// -race plus the knowledge columns: 21-32 s on a loaded laptop, but
		// 66-73 s on a shared two-core CI runner, five times out of six
		// since 2026-09-06 with no change to this package. The budget is
		// there to catch an accidental quadratic, which would take many
		// minutes, not to grade the runner.
		limit = 3 * time.Minute
	}
	assert.Less(t, elapsed, limit, "Apply of %d operations took %s", n, elapsed)

	ops2, err := c.ListOperations(ctx, "bench-service")
	require.NoError(t, err)
	assert.Len(t, ops2, n)

	var ftsCount int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations_fts`).Scan(&ftsCount))
	assert.Equal(t, n, ftsCount)
}
