package catalog_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/catalog"
	"github.com/gs-sinha/sapien/internal/store"
)

func TestApply_PopulatesDocTextFromDocRefs(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	var docText string
	require.NoError(t, db.SQL().QueryRowContext(ctx,
		`SELECT doc_text FROM operations_fts WHERE id = ?`, "rider-service.getRider",
	).Scan(&docText))
	assert.Equal(t, "Overview\nRiders are matched via the getRider operation.", docText)

	// order-service.createOrder is referenced by the "Cancellation" section
	// of order-service's own doc.
	require.NoError(t, db.SQL().QueryRowContext(ctx,
		`SELECT doc_text FROM operations_fts WHERE id = ?`, "order-service.createOrder",
	).Scan(&docText))
	assert.Equal(t, "Cancellation\nSee order-service.createOrder for creation.", docText)

	// rider-service.listRiders is referenced by no doc section at all.
	require.NoError(t, db.SQL().QueryRowContext(ctx,
		`SELECT doc_text FROM operations_fts WHERE id = ?`, "rider-service.listRiders",
	).Scan(&docText))
	assert.Empty(t, docText)

	// memory_text starts empty: no memories exist yet.
	var memText string
	require.NoError(t, db.SQL().QueryRowContext(ctx,
		`SELECT memory_text FROM operations_fts WHERE id = ?`, "rider-service.getRider",
	).Scan(&memText))
	assert.Empty(t, memText)
}

// insertMemory writes a minimal, directly-constructed memories +
// memory_subjects row pair, the same shape internal/memory's Store would
// produce, without depending on that package (this task owns only
// internal/catalog here, and RefreshOperationKnowledge's contract is "read
// whatever is in the memories/memory_subjects tables").
func insertMemory(t *testing.T, db *store.DB, id, body, status string, subjects map[string]string) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO memories (id, scope, type, subject_json, tags_json, source_json, status, body, created, updated, hash)
			VALUES (?, 'workspace', 'note', '{}', '[]', '{}', ?, ?, ?, ?, 'h')
		`, id, status, body, now, now); err != nil {
			return err
		}
		for kind, value := range subjects {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO memory_subjects (memory_id, kind, value) VALUES (?, ?, ?)`,
				id, kind, value,
			); err != nil {
				return err
			}
		}
		return nil
	}))
}

func TestRefreshOperationKnowledge_MemoryText(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	insertMemory(t, db, "mem_1", "qcomSkill indicates QCOM eligibility, not online status.", "active",
		map[string]string{"operation": "rider-service.getRider"})
	// Field-scoped memory: subject.operation + subject.field, keyed
	// "<operation>#<field-path>" per internal/memory's subjectRows.
	insertMemory(t, db, "mem_2", "This field is nullable until the first trip completes.", "active",
		map[string]string{"field": "rider-service.getRider#response.200.body.rating"})
	// Superseded: must not contribute.
	insertMemory(t, db, "mem_3", "Stale note that should not appear.", "superseded",
		map[string]string{"operation": "rider-service.getRider"})
	// Unrelated operation: must not appear in getRider's memory_text.
	insertMemory(t, db, "mem_4", "Unrelated to getRider.", "active",
		map[string]string{"operation": "rider-service.listRiders"})

	require.NoError(t, c.RefreshOperationKnowledge(ctx, []string{"rider-service.getRider"}))

	var memText string
	require.NoError(t, db.SQL().QueryRowContext(ctx,
		`SELECT memory_text FROM operations_fts WHERE id = ?`, "rider-service.getRider",
	).Scan(&memText))
	assert.Contains(t, memText, "QCOM eligibility")
	assert.Contains(t, memText, "nullable until the first trip")
	assert.NotContains(t, memText, "Stale note")
	assert.NotContains(t, memText, "Unrelated to getRider")

	// listRiders' own memory_text is untouched by this call (opIDs scoped
	// it to getRider only) — still empty from Apply.
	var listMemText string
	require.NoError(t, db.SQL().QueryRowContext(ctx,
		`SELECT memory_text FROM operations_fts WHERE id = ?`, "rider-service.listRiders",
	).Scan(&listMemText))
	assert.Empty(t, listMemText)

	// An empty opIDs slice refreshes every operation.
	require.NoError(t, c.RefreshOperationKnowledge(ctx, nil))
	require.NoError(t, db.SQL().QueryRowContext(ctx,
		`SELECT memory_text FROM operations_fts WHERE id = ?`, "rider-service.listRiders",
	).Scan(&listMemText))
	assert.Contains(t, listMemText, "Unrelated to getRider")
}

func TestRefreshOperationKnowledge_TruncationAndCap(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	// A single memory body far longer than 300 characters is truncated to
	// exactly 300 runes.
	longBody := strings.Repeat("a", 500)
	insertMemory(t, db, "mem_long", longBody, "active", map[string]string{"operation": "rider-service.getRider"})
	require.NoError(t, c.RefreshOperationKnowledge(ctx, []string{"rider-service.getRider"}))

	var memText string
	require.NoError(t, db.SQL().QueryRowContext(ctx,
		`SELECT memory_text FROM operations_fts WHERE id = ?`, "rider-service.getRider",
	).Scan(&memText))
	assert.Equal(t, strings.Repeat("a", 300), memText)

	// Many memories together are capped at 2 KB total.
	for i := 0; i < 20; i++ {
		insertMemory(t, db, "mem_cap_"+string(rune('a'+i)), strings.Repeat("b", 300), "active",
			map[string]string{"operation": "rider-service.getRider"})
	}
	require.NoError(t, c.RefreshOperationKnowledge(ctx, []string{"rider-service.getRider"}))
	require.NoError(t, db.SQL().QueryRowContext(ctx,
		`SELECT memory_text FROM operations_fts WHERE id = ?`, "rider-service.getRider",
	).Scan(&memText))
	assert.LessOrEqual(t, len(memText), 2048)
}

func TestReindex_RebuildsFromStoredDataOnly(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)
	insertMemory(t, db, "mem_1", "QCOM eligibility memory.", "active",
		map[string]string{"operation": "rider-service.getRider"})
	require.NoError(t, c.RefreshOperationKnowledge(ctx, nil))

	require.NoError(t, c.Reindex(ctx))

	var opsCount, ftsCount int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations`).Scan(&opsCount))
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations_fts`).Scan(&ftsCount))
	assert.Equal(t, opsCount, ftsCount)

	var docText, memText, opID string
	require.NoError(t, db.SQL().QueryRowContext(ctx,
		`SELECT doc_text, memory_text, op_id FROM operations_fts WHERE id = ?`, "rider-service.getRider",
	).Scan(&docText, &memText, &opID))
	assert.Contains(t, docText, "Overview")
	assert.Contains(t, memText, "QCOM eligibility")
	assert.Contains(t, opID, "getRider")
}

func TestNew_ReindexesOnceAfterKnowledgeSchemaUpgrade(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	c := catalog.New(db)
	applyBaseFixtures(t, c)

	var version string
	require.NoError(t, db.SQL().QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = 'catalog_knowledge_schema_version'`,
	).Scan(&version))
	assert.NotEmpty(t, version)

	// Simulate an existing workspace re-opened after the upgrade: drop the
	// version marker and empty operations_fts (as migration 006 itself
	// would leave it), then open a fresh Catalog against the same db.
	_, err := db.SQL().ExecContext(ctx, `DELETE FROM settings WHERE key = 'catalog_knowledge_schema_version'`)
	require.NoError(t, err)
	_, err = db.SQL().ExecContext(ctx, `DELETE FROM operations_fts`)
	require.NoError(t, err)

	var ftsCountBefore int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations_fts`).Scan(&ftsCountBefore))
	require.Zero(t, ftsCountBefore)

	catalog.New(db) // triggers a full Reindex as a side effect

	var ftsCountAfter, opsCount int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations_fts`).Scan(&ftsCountAfter))
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM operations`).Scan(&opsCount))
	assert.Equal(t, opsCount, ftsCountAfter)
	assert.Greater(t, ftsCountAfter, 0)
}
