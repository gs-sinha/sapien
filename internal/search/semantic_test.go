package search_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/search"
)

// fakeSemantic is a test double for search.Semantic. It records every call
// it receives and returns a fixed set of hits for one particular kind,
// nothing for any other.
type fakeSemantic struct {
	kind string // only respond for this kind ("operation" or "doc")
	hits []search.SemanticHit

	calls     int
	lastKind  string
	lastText  string
	lastLimit int
}

// Query records only calls for f.kind in last{Kind,Text,Limit} (Docs()
// internally re-invokes Operations() for its ref-boost, which — when a
// Searcher's semantic backend is set — issues its own "operation"-kind
// call; a fakeSemantic built for kind "doc" must not let that unrelated
// call clobber what the test wants to observe about the "doc" call). calls
// counts every invocation regardless of kind.
func (f *fakeSemantic) Query(_ context.Context, kind, text string, limit int) ([]search.SemanticHit, error) {
	f.calls++
	if kind != f.kind {
		return nil, nil
	}
	f.lastKind, f.lastText, f.lastLimit = kind, text, limit
	return f.hits, nil
}

// operationFixturesForSemanticTest seeds three operations tailored so the
// query "find couriers near a pickup" (tokens, after stop-word filtering:
// couriers, near, pickup):
//   - lexically matches warehouse-service.findNearbyWarehouses (its summary
//     contains "nearby", a substring hit for the "near" token) and nothing
//     else,
//   - has zero token overlap anywhere in rider-service.searchRiders's
//     indexed fields, so lexical search misses it entirely,
//   - and leaves order-service.createOrder unrelated to either.
func operationFixturesForSemanticTest() []opFixture {
	return []opFixture{
		{
			id: "warehouse-service.findNearbyWarehouses", serviceID: "svc_wh", serviceName: "warehouse-service",
			method: "GET", path: "/v1/warehouses/nearby", rawOpID: "findNearbyWarehouses",
			summary:     "Find nearby warehouses for restocking",
			description: "Locates the nearest warehouse to a given location.",
			tags:        []string{"warehouses", "logistics"},
		},
		{
			id: "rider-service.searchRiders", serviceID: "svc_rider", serviceName: "rider-service",
			method: "POST", path: "/v1/riders/search", rawOpID: "searchRiders",
			summary:     "Search for available riders by skill and radius",
			description: "Finds candidate riders for an active order.",
			tags:        []string{"riders", "search"},
		},
		{
			id: "order-service.createOrder", serviceID: "svc_order", serviceName: "order-service",
			method: "POST", path: "/v1/orders", rawOpID: "createOrder",
			summary:     "Create an order",
			description: "Places a new order for a customer.",
			tags:        []string{"orders", "write"},
		},
	}
}

const semanticTestQuery = "find couriers near a pickup"

func TestOperations_SemanticNilLeavesResultsUnchanged(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())

	baseline, err := search.New(db).Operations(context.Background(), "allocate rider", domain.SearchOptions{})
	require.NoError(t, err)

	withNilSem, err := search.New(db).WithSemantic(nil).Operations(context.Background(), "allocate rider", domain.SearchOptions{})
	require.NoError(t, err)

	assert.Equal(t, baseline, withNilSem)
}

func TestOperations_LexicalAloneMissesSemanticOnlyOperation(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, operationFixturesForSemanticTest())
	s := search.New(db) // no semantic backend

	results, err := s.Operations(context.Background(), semanticTestQuery, domain.SearchOptions{})
	require.NoError(t, err)

	for _, r := range results {
		assert.NotEqual(t, "rider-service.searchRiders", r.Operation.ID,
			"lexical search should not find an operation with zero token overlap")
	}
}

func TestOperations_SemanticFusionAddsMissedHit(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, operationFixturesForSemanticTest())

	sem := &fakeSemantic{
		kind: "operation",
		hits: []search.SemanticHit{{ID: "rider-service.searchRiders", Score: 0.91}},
	}
	s := search.New(db).WithSemantic(sem)

	results, err := s.Operations(context.Background(), semanticTestQuery, domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)

	assert.Equal(t, 1, sem.calls)
	assert.Equal(t, "operation", sem.lastKind)
	assert.Equal(t, semanticTestQuery, sem.lastText)
	assert.Positive(t, sem.lastLimit)

	var found *domain.SearchResult
	for i := range results {
		if results[i].Operation.ID == "rider-service.searchRiders" {
			found = &results[i]
		}
	}
	require.NotNil(t, found, "the fused result must include the semantic-only hit")
	assert.Contains(t, found.MatchedOn, "semantic")

	// The top score in a fused result set is normalized to 1.0.
	top := results[0].Score
	assert.InDelta(t, 1.0, top, 1e-9)

	// The purely-lexical hit (warehouse-service.findNearbyWarehouses) must
	// still be present and must NOT be tagged "semantic".
	var lexHit *domain.SearchResult
	for i := range results {
		if results[i].Operation.ID == "warehouse-service.findNearbyWarehouses" {
			lexHit = &results[i]
		}
	}
	require.NotNil(t, lexHit, "the genuine lexical hit must still be present alongside the fused semantic hit")
	assert.NotContains(t, lexHit.MatchedOn, "semantic")
}

func TestOperations_SemanticRespectsServiceFilter(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, operationFixturesForSemanticTest())

	sem := &fakeSemantic{
		kind: "operation",
		hits: []search.SemanticHit{{ID: "rider-service.searchRiders", Score: 0.9}},
	}
	s := search.New(db).WithSemantic(sem)

	// Filtering to a different service must drop the semantic hit even
	// though the fake backend still "returns" it.
	results, err := s.Operations(context.Background(), semanticTestQuery, domain.SearchOptions{Service: "warehouse-service"})
	require.NoError(t, err)
	for _, r := range results {
		assert.NotEqual(t, "rider-service.searchRiders", r.Operation.ID)
	}
}

func TestOperations_SemanticQueryErrorPropagates(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, operationFixturesForSemanticTest())

	s := search.New(db).WithSemantic(erroringSemantic{})
	_, err := s.Operations(context.Background(), semanticTestQuery, domain.SearchOptions{})
	assert.Error(t, err)
}

type erroringSemantic struct{}

func (erroringSemantic) Query(context.Context, string, string, int) ([]search.SemanticHit, error) {
	return nil, errors.New("fake semantic backend failure")
}

func TestDocs_SemanticNilLeavesResultsUnchanged(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	seedDocs(t, db, logisticsDocs())

	baseline, err := search.New(db).Docs(context.Background(), "allocation rules", domain.SearchOptions{})
	require.NoError(t, err)

	withNilSem, err := search.New(db).WithSemantic(nil).Docs(context.Background(), "allocation rules", domain.SearchOptions{})
	require.NoError(t, err)

	assert.Equal(t, baseline, withNilSem)
}

func TestDocs_SemanticFusionAddsMissedHit(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	seedDocs(t, db, logisticsDocs())

	// "sec_rider_profile" ("Each rider profile includes contact info and
	// vehicle details.") shares no tokens with this query, so lexical Docs()
	// alone must miss it.
	query := "how do couriers get paid"

	lexicalOnly, err := search.New(db).Docs(context.Background(), query, domain.SearchOptions{})
	require.NoError(t, err)
	for _, r := range lexicalOnly {
		assert.NotEqual(t, "sec_rider_profile", r.SectionID)
	}

	sem := &fakeSemantic{
		kind: "doc",
		hits: []search.SemanticHit{{ID: "sec_rider_profile", Score: 0.8}},
	}
	s := search.New(db).WithSemantic(sem)

	results, err := s.Docs(context.Background(), query, domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "doc", sem.lastKind)

	var found bool
	for _, r := range results {
		if r.SectionID == "sec_rider_profile" {
			found = true
		}
	}
	assert.True(t, found, "the fused doc results must include the semantic-only hit")

	top := results[0].Score
	assert.InDelta(t, 1.0, top, 1e-9)
}
