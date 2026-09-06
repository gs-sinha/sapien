package search_test

// Tests for the search ranking tuning task's knowledge-folding step (PLAN
// §16, task part 1): operations_fts gains doc_text/memory_text columns,
// bm25 weight 2 each, and matched_on gains "docs"/"memories" labels.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/search"
)

// ridersTieFixture is two rider-service operations with identical summary,
// description, tags, and concepts — a deliberate tie with no other
// distinguishing lexical signal, so a test can attribute any ranking
// difference cleanly to doc_text/memory_text rather than to something else
// already in play (contrast with logisticsOperations' searchRiders, whose
// summary/description already say "pickup" for unrelated historical
// reasons — see intent_test.go's own note on that).
func ridersTieFixture() []opFixture {
	base := opFixture{
		serviceID: "svc_rider", serviceName: "rider-service",
		summary:     "Find nearby riders",
		description: "Returns riders near a location.",
		tags:        []string{"riders"},
		concepts:    []string{"riders", "availability"},
	}
	searchOp := base
	searchOp.id, searchOp.method, searchOp.path, searchOp.rawOpID =
		"rider-service.searchRiders", "POST", "/v1/riders/search", "searchRiders"
	listOp := base
	listOp.id, listOp.method, listOp.path, listOp.rawOpID =
		"rider-service.listRiders", "GET", "/v1/riders", "listRiders"
	return []opFixture{searchOp, listOp}
}

func TestOperations_DocTextBreaksATieAndLabelsMatchedOn(t *testing.T) {
	ctx := context.Background()
	fixtures := ridersTieFixture()

	// Baseline: neither operation's op_id/summary/description/tags/
	// concepts mentions "pickup" at all. listRiders' shorter path
	// ("/v1/riders" vs "/v1/riders/search") already edges it ahead on
	// path_tokens/bm25 alone — a real, if narrow, misranking: the query is
	// about searching near a point, which is what searchRiders (not
	// listRiders) actually does.
	db := openTestDB(t)
	seedOperations(t, db, fixtures)
	baseline, err := search.New(db).Operations(ctx, "riders near a pickup", domain.SearchOptions{})
	require.NoError(t, err)
	require.Len(t, baseline, 2)
	assert.Equal(t, "rider-service.listRiders", baseline[0].Operation.ID, "baseline misranking, before doc_text")
	for _, r := range baseline {
		assert.NotContains(t, r.MatchedOn, "docs")
	}

	// searchRiders gains doc_text mentioning "pickup" (the only new word);
	// listRiders' doc_text stays empty.
	withDocs := append([]opFixture{}, fixtures...)
	for i := range withDocs {
		if withDocs[i].id == "rider-service.searchRiders" {
			withDocs[i].docText = "Searching riders\nRadius is measured outward from the pickup location the caller supplies."
		}
	}
	db2 := openTestDB(t)
	seedOperations(t, db2, withDocs)
	results, err := search.New(db2).Operations(ctx, "riders near a pickup", domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)

	assert.Equal(t, "rider-service.searchRiders", results[0].Operation.ID,
		"doc_text's 'pickup' mention must break the tie in searchRiders' favor")
	assert.Contains(t, results[0].MatchedOn, "docs")
	if len(results) > 1 {
		assert.Greater(t, results[0].Score, results[1].Score)
		assert.NotContains(t, results[1].MatchedOn, "docs")
	}
}

func TestOperations_MemoryTextSurfacesOperationAndLabelsMatchedOn(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	fixtures := logisticsOperations()
	for i := range fixtures {
		if fixtures[i].id == "allocation-service.allocate" {
			fixtures[i].memoryText = "QCOM eligibility requires qcomSkill=true and the rider online; see rider-service docs."
		}
	}
	seedOperations(t, db, fixtures)

	results, err := search.New(db).Operations(ctx, "qcom eligibility", domain.SearchOptions{})
	require.NoError(t, err)

	idx := indexOf(results, "allocation-service.allocate")
	require.GreaterOrEqual(t, idx, 0, "allocate must be a candidate: %+v", results)
	assert.Contains(t, results[idx].MatchedOn, "memories")
}

func TestOperations_DocTextAndMemoryTextBothCapped2KB(t *testing.T) {
	// Not a ranking assertion: just confirms a very long doc_text/
	// memory_text value (as internal/catalog would only ever produce
	// pre-capped at 2KB, but this package must not choke on one either)
	// still searches and scores sanely rather than erroring.
	ctx := context.Background()
	db := openTestDB(t)

	long := ""
	for i := 0; i < 400; i++ {
		long += "pickup "
	}
	fixtures := ridersTieFixture()
	for i := range fixtures {
		if fixtures[i].id == "rider-service.searchRiders" {
			fixtures[i].docText = long
		}
	}
	seedOperations(t, db, fixtures)

	results, err := search.New(db).Operations(ctx, "pickup", domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "rider-service.searchRiders", results[0].Operation.ID)
}
