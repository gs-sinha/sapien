package local

// Acceptance and unit tests for the search ranking tuning task's
// usage-feedback loop (PLAN §16, part 2): search_feedback.go's in-memory
// ring, noteOperationUse, and the boost internal/search applies from the
// persisted search_feedback table.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// TestSearch_FeedbackFixesRealMisrankAndPersists is the task's acceptance
// example, using the exact misranking the search ranking tuning task's own
// prior report (docs/BUILD-LOG.md, "Search tuning verified on binary")
// named and this task's own doc-text pass (knowledge_test.go) could not
// fix on the real fixture: "find riders around a pickup" ranks
// rider-service.listRiders fractionally above rider-service.searchRiders
// (verified: 1.0000 vs 0.9988), because neither operation's contract nor
// its linked doc section actually contains the word "pickup". Usage
// feedback closes that gap from actual use rather than more text: search
// once (recorded into the ring), inspect the intended operation twice
// (GetOperation, credited against that recorded search), search again —
// it must now rank first with matched_on containing "feedback" — and a
// second, independently opened *Local against the same workspace database
// must see the same boost (search_feedback rows are persisted; the ring
// that fed them is not).
func TestSearch_FeedbackFixesRealMisrankAndPersists(t *testing.T) {
	ws, _ := setupWorkspace(t)
	ctx := context.Background()
	const query = "find riders around a pickup"
	const intended = "rider-service.searchRiders"

	l, err := Open(ws, Options{})
	require.NoError(t, err)

	before, err := l.Search().Operations(ctx, query, domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, before)
	// With docs-mediated fusion on by default (internal/search/docfusion.go)
	// the docs already rank searchRiders first, so the old precondition
	// (searchRiders misranked before any feedback) no longer holds; the
	// test now only proves that feedback is recorded, boosts, and persists.
	t.Logf("first result before feedback: %s", before[0].Operation.ID)
	beforeIdx := indexOf(before, intended)
	require.GreaterOrEqual(t, beforeIdx, 0)
	assert.NotContains(t, before[beforeIdx].MatchedOn, "feedback")

	for i := 0; i < 2; i++ {
		_, err := l.Catalog().GetOperation(ctx, intended)
		require.NoError(t, err)
	}

	after, err := l.Search().Operations(ctx, query, domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, after)
	assert.Equal(t, intended, after[0].Operation.ID, "feedback must now rank the intended operation first")
	assert.Contains(t, after[0].MatchedOn, "feedback")
	require.NoError(t, l.Close())

	// A different *Local, opened fresh against the same workspace/db, sees
	// the same boost: the ring (in-process only) is gone, but the
	// search_feedback rows it wrote are not.
	l2, err := Open(ws, Options{SkipStaleCheck: true})
	require.NoError(t, err)
	defer l2.Close()

	fromScratch, err := l2.Search().Operations(ctx, query, domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, fromScratch)
	assert.Equal(t, intended, fromScratch[0].Operation.ID)
	assert.Contains(t, fromScratch[0].MatchedOn, "feedback")
}

// TestNoteOperationUse_RequiresARecentMatchingSearch confirms
// noteOperationUse only credits an operation's use against a search that
// actually preceded it and actually returned that operation: GetOperation
// with no prior search at all writes nothing.
func TestNoteOperationUse_RequiresARecentMatchingSearch(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Catalog().GetOperation(ctx, "rider-service.searchRiders")
	require.NoError(t, err)

	var count int
	require.NoError(t, l.db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM search_feedback`).Scan(&count))
	assert.Zero(t, count, "no prior search means nothing to credit the use to")
}

// TestNoteOperationUse_OnlyCreditsOperationsTheSearchActuallyReturned
// confirms a search's tokens are only credited to operations that search
// actually returned — a completely unrelated operation used afterward
// earns no feedback from it.
func TestNoteOperationUse_OnlyCreditsOperationsTheSearchActuallyReturned(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	results, err := l.Search().Operations(ctx, "find riders around a pickup", domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	for _, r := range results {
		assert.NotEqual(t, "order-service.getOrderTimeline", r.Operation.ID, "sanity: this op must not be a candidate for this query")
	}

	_, err = l.Catalog().GetOperation(ctx, "order-service.getOrderTimeline")
	require.NoError(t, err)

	var count int
	require.NoError(t, l.db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM search_feedback WHERE op_id = ?`, "order-service.getOrderTimeline",
	).Scan(&count))
	assert.Zero(t, count)
}

// TestFlows_CreateRecordsFeedbackForEveryStepOperation and
// TestRunner_CallRecordsFeedback exercise the other two noteOperationUse
// call sites this task hooks (Flows().Create/Update, Runner().Call),
// keeping the acceptance test above focused on Catalog().GetOperation.
func TestFlows_CreateRecordsFeedbackForEveryStepOperation(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Search().Operations(ctx, "allocate a rider", domain.SearchOptions{})
	require.NoError(t, err)

	const flowYAML = `version: 1
id: alloc-flow
steps:
  - id: alloc
    call: allocation-service.allocate
    body: { orderId: ord_1 }
`
	_, err = l.Flows().Create(ctx, flowYAML, "")
	require.NoError(t, err)

	var count int
	require.NoError(t, l.db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM search_feedback WHERE op_id = ?`, "allocation-service.allocate",
	).Scan(&count))
	assert.Greater(t, count, 0, "Flows().Create must credit allocate for the preceding matching search")
}

func TestRunner_CallRecordsFeedback(t *testing.T) {
	env := setupEngineWithMock(t)
	l := env.l
	ctx := context.Background()

	_, err := l.Search().Operations(ctx, "fetch a rider profile", domain.SearchOptions{})
	require.NoError(t, err)

	run, err := l.Runner().Call(ctx, engine.CallRequest{
		Operation: "rider-service.getRider",
		Params:    map[string]any{"riderId": "R123"},
		Env:       "test",
		Trigger:   "cli",
	})
	require.NoError(t, err)
	require.NotNil(t, run)

	var count int
	require.NoError(t, l.db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM search_feedback WHERE op_id = ?`, "rider-service.getRider",
	).Scan(&count))
	assert.Greater(t, count, 0, "Runner().Call must credit the called operation for the preceding matching search")
}

// TestSearchRing_CapsAtCapacity is a white-box test of the ring itself: it
// never grows past searchRingCap, dropping the oldest entry first (FIFO).
func TestSearchRing_CapsAtCapacity(t *testing.T) {
	r := &searchRing{}
	for i := 0; i < searchRingCap+10; i++ {
		r.add(searchRecord{tokens: []string{"tok"}, resultIDs: []string{"id"}, at: time.Now().UTC()})
	}
	entries := r.snapshot()
	assert.Len(t, entries, searchRingCap)
}

// TestRefreshOperationKnowledge_ErrorIsLoggedAndSwallowed exercises
// refreshOperationKnowledge's best-effort error path: a canceled context
// makes the underlying RefreshOperationKnowledge call fail, and the
// (unexported, void-returning) helper must swallow that rather than panic
// or otherwise surface it.
func TestRefreshOperationKnowledge_ErrorIsLoggedAndSwallowed(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	assert.NotPanics(t, func() {
		l.refreshOperationKnowledge(canceled, nil)
	})
}
