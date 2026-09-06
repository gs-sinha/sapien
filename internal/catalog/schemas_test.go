package catalog_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/catalog"
)

func TestListSchemas_AllServices(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	all, err := c.ListSchemas(ctx, "")
	require.NoError(t, err)
	require.Len(t, all, 3)
	// Ordered by service_id then name: order-service.{Customer,Order}, rider-service.Rider.
	assert.Equal(t, "Customer", all[0].Name)
	assert.Equal(t, "Order", all[1].Name)
	assert.Equal(t, "Rider", all[2].Name)
}
