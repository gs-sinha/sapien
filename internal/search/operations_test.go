package search_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/search"
)

func operationIDs(results []domain.SearchResult) []string {
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.Operation.ID
	}
	return ids
}

func TestOperations_StructuredExactMatch(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "POST /v1/orders", domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "order-service.createOrder", results[0].Operation.ID)
	assert.Equal(t, 1.0, results[0].Score)
	// The URL lookup also lists the resource's children (/v1/orders/{orderId})
	// below the exact hit, at parent scores further reduced by the method
	// mismatch; nothing else may tie the exact match.
	for _, r := range results[1:] {
		assert.Less(t, r.Score, 1.0, r.Operation.ID)
	}
}

func TestOperations_StructuredTemplatedMatch(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "GET /v1/riders/R123", domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "rider-service.getRider", results[0].Operation.ID)
	assert.InDelta(t, 0.94, results[0].Score, 1e-9) // templated match, one {param} consumed
	// With fewer than three URL hits, lexical hits for the path tokens are
	// appended at reduced scores; they must trail the templated match.
	for _, r := range results[1:] {
		assert.Less(t, r.Score, results[0].Score, r.Operation.ID)
	}
}

func TestOperations_StructuredPrefixMatch(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "/v1/riders", domain.SearchOptions{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 2)
	// A parent path lists the operations one segment below it first, at the
	// parent score (0.7 minus 0.03 per missing segment); lexical hits for the
	// path tokens may follow at lower scores.
	ids := operationIDs(results[:2])
	assert.ElementsMatch(t, []string{"rider-service.getRider", "rider-service.searchRiders"}, ids)
	for _, r := range results[:2] {
		assert.InDelta(t, 0.67, r.Score, 1e-9)
	}
	for _, r := range results[2:] {
		assert.Less(t, r.Score, 0.67, r.Operation.ID)
	}
}

func TestOperations_StructuredExactMatch_ServiceFilter(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "POST /v1/allocations", domain.SearchOptions{Service: "allocation-service"})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "allocation-service.allocate", results[0].Operation.ID)
	for _, r := range results[1:] {
		assert.Less(t, r.Score, results[0].Score, r.Operation.ID)
	}

	// The wrong service excludes the alias match entirely: whatever the
	// lexical fallback finds in rider-service, it is not the allocation
	// operation and carries no url label.
	results, err = s.Operations(context.Background(), "POST /v1/allocations", domain.SearchOptions{Service: "rider-service"})
	require.NoError(t, err)
	for _, r := range results {
		assert.NotEqual(t, "allocation-service.allocate", r.Operation.ID)
		assert.NotContains(t, r.MatchedOn, "url")
	}
}

func TestOperations_StructuredPrefixMatch_MethodAndServiceFilters(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "/v1/riders", domain.SearchOptions{Method: "GET", Service: "rider-service"})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 2)
	assert.Equal(t, "rider-service.getRider", results[0].Operation.ID, "the GET child ranks first")
	assert.Contains(t, results[0].MatchedOn, "method")
	assert.Equal(t, "rider-service.searchRiders", results[1].Operation.ID, "the POST child follows, reduced for the method mismatch")
	assert.Less(t, results[1].Score, results[0].Score)

	// No rider op uses PATCH: the path's operations are still listed so the
	// caller sees what the path offers, but none is labelled as a method match
	// and every score is reduced.
	results, err = s.Operations(context.Background(), "/v1/riders", domain.SearchOptions{Method: "PATCH"})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	for _, r := range results {
		assert.NotContains(t, r.MatchedOn, "method", r.Operation.ID)
		assert.Less(t, r.Score, 0.67, r.Operation.ID)
	}
}

func TestOperations_LexicalAllocateRider(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "allocate rider", domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "allocation-service.allocate", results[0].Operation.ID)
}

func TestOperations_LexicalCreateOrder(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "create order", domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "order-service.createOrder", results[0].Operation.ID)
}

func TestOperations_LexicalFieldNameMatch(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "qcomSkill", domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "rider-service.searchRiders", results[0].Operation.ID)
	assert.Contains(t, results[0].MatchedOn, "field:qcomSkill")
}

func TestOperations_LexicalFreeTextDropsStopWords(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "find riders around a pickup", domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "rider-service.searchRiders", results[0].Operation.ID)
}

func TestOperations_LexicalPrefixMatch(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "allocat", domain.SearchOptions{})
	require.NoError(t, err)
	assert.Contains(t, operationIDs(results), "allocation-service.allocate")
}

func TestOperations_DeprecatedRanksBelowSiblingButIsIncluded(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "allocate rider", domain.SearchOptions{})
	require.NoError(t, err)

	var activeIdx, deprecatedIdx = -1, -1
	for i, r := range results {
		switch r.Operation.ID {
		case "allocation-service.allocate":
			activeIdx = i
		case "allocation-service.allocateV1":
			deprecatedIdx = i
		}
	}
	require.GreaterOrEqual(t, activeIdx, 0, "non-deprecated sibling must be present")
	require.GreaterOrEqual(t, deprecatedIdx, 0, "deprecated op must still be included, not excluded")
	assert.Less(t, activeIdx, deprecatedIdx, "deprecated op should rank below its non-deprecated sibling")
	assert.Less(t, results[deprecatedIdx].Score, results[activeIdx].Score)
}

func TestOperations_IncludeDeprecatedSkipsHalving(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	without, err := s.Operations(context.Background(), "allocate rider", domain.SearchOptions{})
	require.NoError(t, err)
	with, err := s.Operations(context.Background(), "allocate rider", domain.SearchOptions{IncludeDeprecated: true})
	require.NoError(t, err)

	scoreOf := func(results []domain.SearchResult, id string) float64 {
		for _, r := range results {
			if r.Operation.ID == id {
				return r.Score
			}
		}
		t.Fatalf("id %q not found in results", id)
		return 0
	}
	assert.Greater(t, scoreOf(with, "allocation-service.allocateV1"), scoreOf(without, "allocation-service.allocateV1"))
}

func TestOperations_ServiceFilter(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "order", domain.SearchOptions{Service: "order-service"})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	for _, r := range results {
		assert.Equal(t, "svc_order", r.Operation.ServiceID)
	}
	assert.NotContains(t, operationIDs(results), "allocation-service.allocate")
}

func TestOperations_MethodFilter(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "order", domain.SearchOptions{Method: "POST"})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	for _, r := range results {
		require.NotNil(t, r.Operation.HTTP)
		assert.Equal(t, "POST", r.Operation.HTTP.Method)
	}
	ids := operationIDs(results)
	assert.Contains(t, ids, "order-service.createOrder")
	assert.NotContains(t, ids, "order-service.getOrder")
	assert.NotContains(t, ids, "order-service.cancelOrder")
}

func TestOperations_WeirdPunctuationDoesNotError(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	_, err := s.Operations(context.Background(), `orders (draft)`, domain.SearchOptions{})
	assert.NoError(t, err)

	_, err = s.Operations(context.Background(), `"quoted" / weird \ query!!`, domain.SearchOptions{})
	assert.NoError(t, err)
}

func TestOperations_EmptyQuery(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "", domain.SearchOptions{})
	require.NoError(t, err)
	assert.Nil(t, results)

	results, err = s.Operations(context.Background(), "   ", domain.SearchOptions{})
	require.NoError(t, err)
	assert.Nil(t, results)
}

func TestOperations_Limit(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)

	results, err := s.Operations(context.Background(), "order", domain.SearchOptions{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, results, 1)
}
