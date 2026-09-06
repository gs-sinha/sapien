package semantic_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/semantic"
	"github.com/gs-sinha/sapien/internal/store"
)

// openTestDB opens a fresh, migrated in-memory store for one test.
func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newTestEmbedder returns a fake Embedder that hash-embeds text into 16
// dims (see hashEmbed in embedder_test.go) without any network call, so
// index tests can exercise Index without an httptest server.
type fakeEmbedder struct {
	model     string
	dim       int
	callCount int
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.callCount++
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = hashEmbed(t)
	}
	if f.dim == 0 && len(out) > 0 {
		f.dim = len(out[0])
	}
	return out, nil
}

func (f *fakeEmbedder) Model() string { return f.model }
func (f *fakeEmbedder) Dim() int      { return f.dim }

func newFakeEmbedder(model string) *fakeEmbedder {
	return &fakeEmbedder{model: model}
}

func riderOp() domain.Operation {
	return domain.Operation{
		ID:          "rider-service.searchRiders",
		HTTP:        &domain.HTTPBinding{Method: "POST", Path: "/v1/riders/search"},
		Summary:     "Find riders around a pickup location",
		Description: "Searches for available riders near a pickup point matching skill and radius.",
		Tags:        []string{"riders", "search"},
	}
}

func orderOp() domain.Operation {
	return domain.Operation{
		ID:          "order-service.createOrder",
		HTTP:        &domain.HTTPBinding{Method: "POST", Path: "/v1/orders"},
		Summary:     "Create an order",
		Description: "Places a new order for a customer.",
		Tags:        []string{"orders", "write"},
	}
}

func TestIndexOperations_UpsertAndSkipUnchanged(t *testing.T) {
	db := openTestDB(t)
	emb := newFakeEmbedder("test-model")
	idx := semantic.NewIndex(db, emb)
	ctx := context.Background()

	ops := []domain.Operation{riderOp(), orderOp()}

	n, err := idx.IndexOperations(ctx, ops, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "first index run writes both operations")

	stats, err := idx.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.Total)
	assert.Equal(t, 2, stats.ByKind[semantic.KindOperation])
	require.Len(t, stats.Models, 1)
	assert.Equal(t, "test-model", stats.Models[0].Model)
	assert.Equal(t, 2, stats.Models[0].Count)

	// Re-indexing identical content should skip both (no embed calls, no
	// rows rewritten).
	callsBefore := emb.callCount
	n, err = idx.IndexOperations(ctx, ops, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "unchanged content should be skipped")
	assert.Equal(t, callsBefore, emb.callCount, "skip-unchanged must not call Embed again")

	// Changing one operation's summary should cause exactly one re-embed.
	changed := ops[0]
	changed.Summary = "Find couriers near a pickup"
	n, err = idx.IndexOperations(ctx, []domain.Operation{changed, ops[1]}, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "only the changed operation should be rewritten")
}

func TestIndexOperations_FieldsContributeToText(t *testing.T) {
	db := openTestDB(t)
	emb := newFakeEmbedder("m")
	idx := semantic.NewIndex(db, emb)
	ctx := context.Background()

	op := riderOp()
	fields := map[string][]domain.Field{
		op.ID: {
			{OperationID: op.ID, Path: "request.body.qcomSkill", Type: "string"},
			{OperationID: op.ID, Path: "request.body.items[].riderId", Type: "string"},
		},
	}

	n, err := idx.IndexOperations(ctx, []domain.Operation{op}, fields)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// Changing only the fields map (same operation content otherwise)
	// should invalidate the content hash and cause a re-embed.
	n, err = idx.IndexOperations(ctx, []domain.Operation{op}, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "dropping the field leaves changes the embedded text")
}

func TestIndexOperations_ModelChangeForcesReembed(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	op := riderOp()

	emb1 := newFakeEmbedder("model-a")
	idx1 := semantic.NewIndex(db, emb1)
	n, err := idx1.IndexOperations(ctx, []domain.Operation{op}, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// A different Index using a different model must not treat the row as
	// unchanged, even though the text is identical.
	emb2 := newFakeEmbedder("model-b")
	idx2 := semantic.NewIndex(db, emb2)
	n, err = idx2.IndexOperations(ctx, []domain.Operation{op}, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "a model change must force a re-embed")

	stats, err := idx2.Stats(ctx)
	require.NoError(t, err)
	// Both models' rows exist under the same (kind, id) primary key, so the
	// second write replaced the first: only one row, now under model-b.
	assert.Equal(t, 1, stats.Total)
	require.Len(t, stats.Models, 1)
	assert.Equal(t, "model-b", stats.Models[0].Model)
}

func TestIndexOperations_EmptyInput(t *testing.T) {
	db := openTestDB(t)
	idx := semantic.NewIndex(db, newFakeEmbedder("m"))
	n, err := idx.IndexOperations(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestIndexDocs_UpsertAndSkipUnchanged(t *testing.T) {
	db := openTestDB(t)
	emb := newFakeEmbedder("m")
	idx := semantic.NewIndex(db, emb)
	ctx := context.Background()

	sections := []semantic.DocSectionInput{
		{ID: "sec_1", Title: "Rider Matching Guide", Heading: "Rider search filters",
			Body: "Search riders using qcomSkill and radius filters to narrow candidates."},
		{ID: "sec_2", Title: "Allocation Guide", Heading: "Retry policy",
			Body: "If allocation fails the system retries up to three times before escalating."},
	}

	n, err := idx.IndexDocs(ctx, sections)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	n, err = idx.IndexDocs(ctx, sections)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	stats, err := idx.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.ByKind[semantic.KindDoc])
}

func TestIndexDocs_BodyTruncatedAt1000Chars(t *testing.T) {
	db := openTestDB(t)
	emb := newFakeEmbedder("m")
	idx := semantic.NewIndex(db, emb)
	ctx := context.Background()

	longBody := make([]byte, 2000)
	for i := range longBody {
		longBody[i] = 'a'
	}
	// Put a distinguishing word past the 1,000 char cutoff.
	tail := "distinguishingtail"
	body := string(longBody[:1000-1]) + " " + tail + string(longBody[:900])

	n, err := idx.IndexDocs(ctx, []semantic.DocSectionInput{{ID: "sec", Title: "T", Heading: "H", Body: body}})
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// A doc whose body is identical only up to the first 1000 chars should
	// hash the same (the tail is dropped), so re-indexing with a different
	// tail is a no-op.
	bodyDifferentTail := string(longBody[:1000-1]) + " " + tail + "-completely-different-suffix"
	n, err = idx.IndexDocs(ctx, []semantic.DocSectionInput{{ID: "sec", Title: "T", Heading: "H", Body: bodyDifferentTail}})
	require.NoError(t, err)
	assert.Equal(t, 0, n, "content past the first 1000 chars must not affect the hash")
}

func TestIndexMemories_UpsertAndSkipUnchanged(t *testing.T) {
	db := openTestDB(t)
	emb := newFakeEmbedder("m")
	idx := semantic.NewIndex(db, emb)
	ctx := context.Background()

	mems := []domain.Memory{
		{ID: "mem_1", Text: "Retries should back off exponentially.", Tags: []string{"gotcha", "retry"}},
		{ID: "mem_2", Text: "The rider service caches skill lookups for 5 minutes.", Tags: []string{"riders"}},
	}

	n, err := idx.IndexMemories(ctx, mems)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	n, err = idx.IndexMemories(ctx, mems)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	stats, err := idx.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.ByKind[semantic.KindMemory])
}

func TestQuery_RanksSharedWordsFirst(t *testing.T) {
	db := openTestDB(t)
	emb := newFakeEmbedder("m")
	idx := semantic.NewIndex(db, emb)
	ctx := context.Background()

	ops := []domain.Operation{riderOp(), orderOp()}
	_, err := idx.IndexOperations(ctx, ops, nil)
	require.NoError(t, err)

	hits, err := idx.Query(ctx, semantic.KindOperation, "find couriers near a pickup", 5)
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	assert.Equal(t, "rider-service.searchRiders", hits[0].ID,
		"a query sharing words with the rider search operation should rank it first")
}

func TestQuery_RespectsLimit(t *testing.T) {
	db := openTestDB(t)
	emb := newFakeEmbedder("m")
	idx := semantic.NewIndex(db, emb)
	ctx := context.Background()

	_, err := idx.IndexOperations(ctx, []domain.Operation{riderOp(), orderOp()}, nil)
	require.NoError(t, err)

	hits, err := idx.Query(ctx, semantic.KindOperation, "order rider search", 1)
	require.NoError(t, err)
	assert.Len(t, hits, 1)
}

func TestQuery_EmptyTextOrNonPositiveLimit(t *testing.T) {
	db := openTestDB(t)
	idx := semantic.NewIndex(db, newFakeEmbedder("m"))
	ctx := context.Background()

	hits, err := idx.Query(ctx, semantic.KindOperation, "   ", 5)
	require.NoError(t, err)
	assert.Nil(t, hits)

	hits, err = idx.Query(ctx, semantic.KindOperation, "anything", 0)
	require.NoError(t, err)
	assert.Nil(t, hits)
}

func TestQuery_IgnoresRowsFromADifferentModelOrDim(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	embOld := newFakeEmbedder("old-model")
	idxOld := semantic.NewIndex(db, embOld)
	_, err := idxOld.IndexOperations(ctx, []domain.Operation{riderOp()}, nil)
	require.NoError(t, err)

	embNew := newFakeEmbedder("new-model")
	idxNew := semantic.NewIndex(db, embNew)
	// No rows indexed under the new model/embedder yet: Query must not
	// return the stale old-model row.
	hits, err := idxNew.Query(ctx, semantic.KindOperation, "find couriers near a pickup", 5)
	require.NoError(t, err)
	assert.Empty(t, hits, "a differently-modeled row must be ignored")
}

func TestQuery_UnknownKindIsEmpty(t *testing.T) {
	db := openTestDB(t)
	emb := newFakeEmbedder("m")
	idx := semantic.NewIndex(db, emb)
	ctx := context.Background()

	_, err := idx.IndexOperations(ctx, []domain.Operation{riderOp()}, nil)
	require.NoError(t, err)

	hits, err := idx.Query(ctx, semantic.KindDoc, "find couriers near a pickup", 5)
	require.NoError(t, err)
	assert.Empty(t, hits)
}

func TestClear(t *testing.T) {
	db := openTestDB(t)
	emb := newFakeEmbedder("m")
	idx := semantic.NewIndex(db, emb)
	ctx := context.Background()

	_, err := idx.IndexOperations(ctx, []domain.Operation{riderOp(), orderOp()}, nil)
	require.NoError(t, err)
	_, err = idx.IndexMemories(ctx, []domain.Memory{{ID: "mem_1", Text: "note"}})
	require.NoError(t, err)

	require.NoError(t, idx.Clear(ctx, semantic.KindOperation))

	stats, err := idx.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.ByKind[semantic.KindOperation])
	assert.Equal(t, 1, stats.ByKind[semantic.KindMemory], "Clear must only affect the given kind")
}

func TestStats_EmptyIndex(t *testing.T) {
	db := openTestDB(t)
	idx := semantic.NewIndex(db, newFakeEmbedder("m"))
	stats, err := idx.Stats(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Total)
	assert.Empty(t, stats.Models)
}
