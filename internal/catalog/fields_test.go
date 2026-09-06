package catalog_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/catalog"
)

func TestFieldsByName(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	fields, err := c.FieldsByName(ctx, "rider-service", "riderId")
	require.NoError(t, err)
	require.Len(t, fields, 3, "getRider's 2 riderId fields + listRiders' 1")
	for _, f := range fields {
		assert.Contains(t, f.Path, "riderId")
	}

	// Case-insensitive.
	fieldsUpper, err := c.FieldsByName(ctx, "rider-service", "RiderId")
	require.NoError(t, err)
	assert.Len(t, fieldsUpper, 3)

	// Scoped to the given service: order-service also has riderId fields but
	// they must not leak in.
	orderFields, err := c.FieldsByName(ctx, "order-service", "riderId")
	require.NoError(t, err)
	require.Len(t, orderFields, 2, "createOrder's request riderId + getOrder's response riderId")
	for _, f := range orderFields {
		assert.Contains(t, []string{"order-service.createOrder", "order-service.getOrder"}, f.OperationID)
	}

	none, err := c.FieldsByName(ctx, "rider-service", "noSuchField")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestFields_LeafArrayStripped(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	ctx := t.Context()
	applyBaseFixtures(t, c)

	fields, err := c.Fields(ctx, "rider-service.listRiders")
	require.NoError(t, err)
	require.Len(t, fields, 2)

	byName, err := c.FieldsByName(ctx, "rider-service", "name")
	require.NoError(t, err)
	var foundArrayField bool
	for _, f := range byName {
		if f.Path == "response.200.body.items[].name" {
			foundArrayField = true
		}
	}
	assert.True(t, foundArrayField, "the '[]' array segment must be stripped before leaf-name matching")
}
