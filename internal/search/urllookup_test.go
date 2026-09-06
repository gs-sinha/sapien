package search_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/search"
)

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

func TestOperations_URLShapedQueries(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	s := search.New(db)
	ctx := context.Background()

	t.Run("full URL with host, concrete id, and query string", func(t *testing.T) {
		res, err := s.Operations(ctx, "https://api.example.com/v1/riders/R123?expand=1", domain.SearchOptions{})
		require.NoError(t, err)
		require.NotEmpty(t, res)
		assert.Equal(t, "rider-service.getRider", res[0].Operation.ID)
		assert.True(t, hasTag(res[0].MatchedOn, "url"), res[0].MatchedOn)
		assert.InDelta(t, 0.94, res[0].Score, 0.001) // templated match, one {param} consumed
	})

	t.Run("method and path: method match is reported", func(t *testing.T) {
		res, err := s.Operations(ctx, "GET /v1/riders/R123", domain.SearchOptions{})
		require.NoError(t, err)
		require.NotEmpty(t, res)
		assert.Equal(t, "rider-service.getRider", res[0].Operation.ID)
		assert.True(t, hasTag(res[0].MatchedOn, "method"), res[0].MatchedOn)
	})

	t.Run("wrong method still finds the path, lower and unlabelled", func(t *testing.T) {
		res, err := s.Operations(ctx, "POST /v1/riders/R123", domain.SearchOptions{})
		require.NoError(t, err)
		require.NotEmpty(t, res)
		assert.Equal(t, "rider-service.getRider", res[0].Operation.ID)
		assert.False(t, hasTag(res[0].MatchedOn, "method"))
		assert.Less(t, res[0].Score, 0.94)
	})

	t.Run("base path prefix the contract does not have (suffix match)", func(t *testing.T) {
		res, err := s.Operations(ctx, "/logistics/v1/riders/search", domain.SearchOptions{})
		require.NoError(t, err)
		require.NotEmpty(t, res)
		assert.Equal(t, "rider-service.searchRiders", res[0].Operation.ID)
		assert.InDelta(t, 0.85, res[0].Score, 0.001)
	})

	t.Run("no leading slash, parent path lists children below the exact hit", func(t *testing.T) {
		res, err := s.Operations(ctx, "v1/orders", domain.SearchOptions{})
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(res), 3)
		assert.Equal(t, "order-service.createOrder", res[0].Operation.ID)
		assert.InDelta(t, 1.0, res[0].Score, 0.001)
		ids := []string{res[1].Operation.ID, res[2].Operation.ID}
		assert.ElementsMatch(t, []string{"order-service.getOrder", "order-service.cancelOrder"}, ids)
	})

	t.Run("method filter option disambiguates a shared template", func(t *testing.T) {
		res, err := s.Operations(ctx, "/v1/orders/O1", domain.SearchOptions{Method: "delete"})
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(res), 2)
		assert.Equal(t, "order-service.cancelOrder", res[0].Operation.ID)
		assert.Equal(t, "order-service.getOrder", res[1].Operation.ID)
	})

	t.Run("unknown path falls back to lexical, never a url label", func(t *testing.T) {
		res, err := s.Operations(ctx, "/v9/nothing/here", domain.SearchOptions{})
		require.NoError(t, err)
		for _, r := range res {
			assert.False(t, hasTag(r.MatchedOn, "url"), r.Operation.ID)
		}
	})
}
