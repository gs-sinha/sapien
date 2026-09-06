package catalog_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/catalog"
)

func TestSuggestOperationIDs_Ranking(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	suggestions, err := c.SuggestOperationIDs(ctx, "rider-service.getRiders", 5)
	require.NoError(t, err)
	require.NotEmpty(t, suggestions)
	assert.Equal(t, "rider-service.getRider", suggestions[0], "single-character typo should rank first")
}

func TestSuggestOperationIDs_LimitAndDeterminism(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	s1, err := c.SuggestOperationIDs(ctx, "rider-service.getRiders", 2)
	require.NoError(t, err)
	assert.Len(t, s1, 2)

	s2, err := c.SuggestOperationIDs(ctx, "rider-service.getRiders", 2)
	require.NoError(t, err)
	assert.Equal(t, s1, s2, "identical input must produce identical output")

	zero, err := c.SuggestOperationIDs(ctx, "anything", 0)
	require.NoError(t, err)
	assert.Empty(t, zero)
}

func TestSuggestOperationIDs_EmptyCatalog(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()

	suggestions, err := c.SuggestOperationIDs(ctx, "anything", 5)
	require.NoError(t, err)
	assert.Empty(t, suggestions)
}
