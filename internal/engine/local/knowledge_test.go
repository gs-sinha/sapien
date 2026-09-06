package local

// Acceptance tests for the search ranking tuning task's knowledge-folding
// step (PLAN §16, part 1): operations_fts' doc_text/memory_text columns,
// populated by internal/catalog from real doc content and from memories,
// verified against the real fixtures/logistics package (not a synthetic
// fixture) through a fully opened *Local.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
)

// TestSearch_DocTextLabelsMatchedOnDocs is grounded in what
// fixtures/logistics/allocation-service/api/openapi.yaml and docs/
// allocation.md actually say (verified by reading them, not assumed):
// allocation-service.getAllocation's own contract carries no `description`
// at all (only a summary, "Fetch an allocation by ID"), but the doc's
// "Inspecting allocations" section gives it real substance — "are
// read-only lookups" — that appears nowhere in its contract text. Before
// this task, getAllocation already narrowly edged out its allocation-
// service siblings for this query (op_id/tags alone); doc_text widens that
// margin and, this test's real assertion, must be why: matched_on for the
// winner must contain "docs".
//
// NOTE on the task's other named example ("find riders around a pickup"
// must rank searchRiders over listRiders via doc_text): verified against
// the real fixture and it does not hold — no doc section in
// fixtures/logistics/rider-service/api/docs/riders.md that mentions
// "pickup" (only the unrelated "upcomingTrips" section does, and no
// operation ID appears anywhere near it, so doc_refs never links it to any
// operation) references rider-service.searchRiders at all; searchRiders'
// own contract text ("Search for nearby riders" / "Returns riders near a
// point...") never says "pickup" either. See search_feedback_test.go for
// where that specific example is resolved instead (part 2, usage
// feedback), per this task's own instruction to adjust the assertion to
// what the fixture docs actually say.
func TestSearch_DocTextLabelsMatchedOnDocs(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	results, err := l.Search().Operations(ctx, "read-only allocation lookups", domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)

	assert.Equal(t, "allocation-service.getAllocation", results[0].Operation.ID)
	assert.Contains(t, results[0].MatchedOn, "docs")
}

// TestSearch_MemoryAddsMatchedOnMemories is the task's other named
// acceptance example: a memory attached to allocation-service.allocate
// whose text says "QCOM eligibility" makes the query "qcom eligibility"
// return allocate with matched_on containing "memories". Verified before
// and after: before the memory exists, allocate is a middling candidate
// (rider-service operations rank above it, since their tags/fields already
// match "qcom"); after, memory_text's extra weight brings it to the top.
func TestSearch_MemoryAddsMatchedOnMemories(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	before, err := l.Search().Operations(ctx, "qcom eligibility", domain.SearchOptions{})
	require.NoError(t, err)
	beforeIdx := indexOf(before, "allocation-service.allocate")
	require.GreaterOrEqual(t, beforeIdx, 0, "allocate must already be a candidate: %+v", before)
	for _, r := range before {
		if r.Operation.ID == "allocation-service.allocate" {
			assert.NotContains(t, r.MatchedOn, "memories", "no memory exists yet")
		}
	}

	_, err = l.Memories().Create(ctx, domain.Memory{
		Type:    domain.MemorySemantic,
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Operation: "allocation-service.allocate"},
		Tags:    []string{"qcom", "allocation"},
		Text:    "QCOM eligibility requires qcomSkill=true on the rider and the rider must be online.",
	})
	require.NoError(t, err)

	after, err := l.Search().Operations(ctx, "qcom eligibility", domain.SearchOptions{})
	require.NoError(t, err)
	afterIdx := indexOf(after, "allocation-service.allocate")
	require.GreaterOrEqual(t, afterIdx, 0)
	assert.Contains(t, after[afterIdx].MatchedOn, "memories")
	assert.Greater(t, after[afterIdx].Score, before[beforeIdx].Score,
		"the memory must measurably improve allocate's score for this query")
}

// indexOf returns the index of id in results, or -1.
func indexOf(results []domain.SearchResult, id string) int {
	for i, r := range results {
		if r.Operation.ID == id {
			return i
		}
	}
	return -1
}
