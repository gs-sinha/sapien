package search_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/search"
)

func TestDocs_RefBoostRanksFirst(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	seedDocs(t, db, logisticsDocs())
	s := search.New(db)

	results, err := s.Docs(context.Background(), "allocation rules", domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)

	top := results[0]
	assert.Equal(t, "sec_allocation_rules", top.SectionID)
	assert.Equal(t, "Allocation rules", top.Heading)
	assert.Equal(t, "allocation-service", top.Service)
	assert.NotEmpty(t, top.Snippet, "snippet should be non-empty")

	var hasOpRef bool
	for _, r := range top.Refs {
		if r.Kind == domain.RefOperation && r.Value == "allocation-service.allocate" {
			hasOpRef = true
		}
	}
	assert.True(t, hasOpRef, "top section should reference allocation-service.allocate")
}

func TestDocs_ServiceFilter(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	seedDocs(t, db, logisticsDocs())
	s := search.New(db)

	all, err := s.Docs(context.Background(), "rider", domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, all)
	// Sanity: without a filter, the allocation doc's "Riders are allocated..."
	// body also matches "rider", so more than one service shows up.
	var services = map[string]bool{}
	for _, r := range all {
		services[r.Service] = true
	}
	require.Len(t, services, 2, "fixture should surface both services for this query")

	filtered, err := s.Docs(context.Background(), "rider", domain.SearchOptions{Service: "rider-service"})
	require.NoError(t, err)
	require.NotEmpty(t, filtered)
	for _, r := range filtered {
		assert.Equal(t, "rider-service", r.Service)
	}
}

func TestDocs_EmptyQuery(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	seedDocs(t, db, logisticsDocs())
	s := search.New(db)

	results, err := s.Docs(context.Background(), "", domain.SearchOptions{})
	require.NoError(t, err)
	assert.Nil(t, results)
}

func TestDocs_Limit(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	seedDocs(t, db, logisticsDocs())
	s := search.New(db)

	results, err := s.Docs(context.Background(), "rider", domain.SearchOptions{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, results, 1)
}
