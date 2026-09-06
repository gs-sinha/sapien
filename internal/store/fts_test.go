package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFTS_OperationsSearchAndBM25(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, err := db.SQL().ExecContext(ctx, `
		INSERT INTO operations_fts (id, service, op_id, path_tokens, summary, description, tags, param_names, field_names)
		VALUES
			('op_rider', 'riders', 'createRider', 'v1 riders', 'Create a rider', 'Creates a new rider record', 'riders write', 'riderId', 'riderId name'),
			('op_order', 'orders', 'createOrder', 'v1 orders', 'Create an order', 'Places a new order for a customer', 'orders write', 'orderId', 'orderId customerId')
	`)
	require.NoError(t, err)

	rows, err := db.SQL().QueryContext(ctx, `
		SELECT id, bm25(operations_fts, 8.0, 1.0, 6.0, 5.0, 1.0, 3.0, 3.0, 3.0) AS score
		FROM operations_fts
		WHERE operations_fts MATCH 'rider'
		ORDER BY score
	`)
	require.NoError(t, err)
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		var score float64
		require.NoError(t, rows.Scan(&id, &score))
		ids = append(ids, id)
		assert.Less(t, score, 0.0, "bm25 scores are negative in SQLite; lower is a better match")
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"op_rider"}, ids)
}

func TestFTS_OperationsTrigramSubstringMatch(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, err := db.SQL().ExecContext(ctx, `
		INSERT INTO operations_trigram (id, op_id, path) VALUES
			('op_rider', 'createRider', '/v1/riders/{riderId}'),
			('op_order', 'createOrder', '/v1/orders/{orderId}')
	`)
	require.NoError(t, err)

	// "ride" (not just "rid") distinguishes "riders/{riderId}" from
	// "orders/{orderId}": lowercased, "orderid" contains "rid" (…orde-RID)
	// but not "ride".
	rows, err := db.SQL().QueryContext(ctx, `
		SELECT id FROM operations_trigram WHERE operations_trigram MATCH 'ride' ORDER BY id
	`)
	require.NoError(t, err)
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"op_rider"}, ids, "substring 'ride' should hit rider paths/ops only")
}

func TestFTS_DocsSearch(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, err := db.SQL().ExecContext(ctx, `
		INSERT INTO docs_fts (section_id, service, title, heading, body) VALUES
			('sec_1', 'riders', 'Rider allocation', 'Allocation windows', 'Riders are allocated within a rolling window based on skill tags.'),
			('sec_2', 'orders', 'Order lifecycle', 'States', 'Orders move through created, allocated, and delivered states.')
	`)
	require.NoError(t, err)

	var sectionID string
	err = db.SQL().QueryRowContext(ctx, `
		SELECT section_id FROM docs_fts WHERE docs_fts MATCH 'allocation' ORDER BY bm25(docs_fts) LIMIT 1
	`).Scan(&sectionID)
	require.NoError(t, err)
	assert.Equal(t, "sec_1", sectionID)
}

func TestFTS_MemoriesSearch(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, err := db.SQL().ExecContext(ctx, `
		INSERT INTO memories_fts (id, body, tags, subject_text) VALUES
			('mem_1', 'The riderId field is nullable when the order has no assigned rider yet.', 'gotcha riders', 'orders.createOrder'),
			('mem_2', 'Pagination cursors expire after 5 minutes.', 'pagination', 'orders.listOrders')
	`)
	require.NoError(t, err)

	var id string
	err = db.SQL().QueryRowContext(ctx, `
		SELECT id FROM memories_fts WHERE memories_fts MATCH 'nullable' ORDER BY bm25(memories_fts) LIMIT 1
	`).Scan(&id)
	require.NoError(t, err)
	assert.Equal(t, "mem_1", id)
}

func TestFTS_DeleteAndReinsertByID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, err := db.SQL().ExecContext(ctx, `
		INSERT INTO operations_fts (id, service, op_id, path_tokens, summary, description, tags, param_names, field_names)
		VALUES ('op_1', 'riders', 'createRider', 'v1 riders', 'Create a rider', '', '', '', '')
	`)
	require.NoError(t, err)

	_, err = db.SQL().ExecContext(ctx, `DELETE FROM operations_fts WHERE id = 'op_1'`)
	require.NoError(t, err)

	var count int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations_fts WHERE id = 'op_1'`).Scan(&count))
	assert.Equal(t, 0, count)

	_, err = db.SQL().ExecContext(ctx, `
		INSERT INTO operations_fts (id, service, op_id, path_tokens, summary, description, tags, param_names, field_names)
		VALUES ('op_1', 'riders', 'updateRider', 'v1 riders', 'Update a rider', '', '', '', '')
	`)
	require.NoError(t, err)

	var summary string
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT summary FROM operations_fts WHERE id = 'op_1'`).Scan(&summary))
	assert.Equal(t, "Update a rider", summary)
}
