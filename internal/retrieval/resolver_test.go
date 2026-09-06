package retrieval_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/memory"
	"github.com/gs-sinha/sapien/internal/retrieval"
)

// TestCatalogResolver_Operation checks CatalogResolver.Operation's
// projection of a known operation: service, method, path, and the union of
// component schemas it uses and OpenAPI tags / service.yaml concepts it
// carries.
func TestCatalogResolver_Operation(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	res := &retrieval.CatalogResolver{Cat: env.cat}

	// CatalogResolver must satisfy memory.Resolver.
	var _ memory.Resolver = res

	info, ok := res.Operation(ctx, "rider-service.getRider")
	require.True(t, ok)
	require.Equal(t, "rider-service", info.Service)
	require.Equal(t, "GET", info.Method)
	require.Equal(t, "/v1/riders/{riderId}", info.Path)
	require.NotEmpty(t, info.Hash)
	require.Contains(t, info.Schemas, "rider-service.Rider")
	require.Contains(t, info.Tags, "riders")       // OpenAPI tag
	require.Contains(t, info.Tags, "availability") // service.yaml concept
	require.Contains(t, info.Tags, "qcom skill")   // service.yaml concept
}

// TestCatalogResolver_Operation_Unknown checks the ok=false path for an
// operation the catalog doesn't know.
func TestCatalogResolver_Operation_Unknown(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	res := &retrieval.CatalogResolver{Cat: env.cat}

	_, ok := res.Operation(ctx, "does-not.exist")
	require.False(t, ok)
}

// TestCatalogResolver_FlowsUsing checks that the smoke flow (which calls
// allocation-service.allocate) is reported as using it.
func TestCatalogResolver_FlowsUsing(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	res := &retrieval.CatalogResolver{Cat: env.cat}

	ids := res.FlowsUsing(ctx, "allocation-service.allocate")
	require.Contains(t, ids, "smoke")

	require.Empty(t, res.FlowsUsing(ctx, "rider-service.listRiders"))
}

// TestCatalogResolver_FieldExists checks both the true and false paths.
func TestCatalogResolver_FieldExists(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	res := &retrieval.CatalogResolver{Cat: env.cat}

	require.True(t, res.FieldExists(ctx, "rider-service.getRider", "response.200.body.qcomSkill"))
	require.False(t, res.FieldExists(ctx, "rider-service.getRider", "response.200.body.notAField"))
	require.False(t, res.FieldExists(ctx, "does-not.exist", "response.200.body.qcomSkill"))
}
