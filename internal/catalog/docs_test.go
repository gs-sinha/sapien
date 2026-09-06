package catalog_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/catalog"
	"github.com/growsimplee/sapien/internal/domain"
)

func TestDocsReferencing(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	refs, err := c.DocsReferencing(ctx, domain.RefOperation, "rider-service.getRider", 0)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, "rider-service/docs/allocation.md", refs[0].DocID)
	assert.Equal(t, "rider-service", refs[0].Service)
	assert.Equal(t, "Overview", refs[0].Section.Heading)

	refs2, err := c.DocsReferencing(ctx, domain.RefOperation, "order-service.createOrder", 0)
	require.NoError(t, err)
	require.Len(t, refs2, 1)
	assert.Equal(t, "Cancellation", refs2[0].Section.Heading)

	none, err := c.DocsReferencing(ctx, domain.RefOperation, "no-such-operation", 0)
	require.NoError(t, err)
	assert.Empty(t, none)

	// limit is respected.
	limited, err := c.DocsReferencing(ctx, domain.RefOperation, "rider-service.getRider", 1)
	require.NoError(t, err)
	assert.Len(t, limited, 1)
}

func TestListDocs_HeadingsWithoutBodies(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	docs, err := c.ListDocs(ctx, "rider-service")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Len(t, docs[0].Sections, 2)
	assert.Equal(t, "Overview", docs[0].Sections[0].Heading)
	assert.Empty(t, docs[0].Sections[0].Body, "ListDocs omits section bodies")
}

func TestGetDoc_NotFound(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	_, err := c.GetDoc(ctx, "rider-service", "docs/does-not-exist.md")
	require.Error(t, err)
}

func TestGetDocSection_NotFound(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	sec, doc, err := c.GetDocSection(ctx, "no-such-section")
	require.NoError(t, err)
	assert.Nil(t, sec)
	assert.Nil(t, doc)
}
