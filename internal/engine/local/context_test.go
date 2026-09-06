package local

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

func TestContext_Build_QCOMAllocation(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)

	bundle, err := l.Context().Build(ctx, domain.ContextRequest{Intent: "Create a QCOM allocation test"})
	require.NoError(t, err)
	require.NotNil(t, bundle)

	opIDs := make([]string, 0, len(bundle.Operations))
	for _, op := range bundle.Operations {
		opIDs = append(opIDs, op.ID)
	}
	assert.Contains(t, opIDs, "allocation-service.allocate")

	memIDs := make([]string, 0, len(bundle.Memories))
	for _, m := range bundle.Memories {
		memIDs = append(memIDs, m.ID)
	}
	assert.Contains(t, memIDs, created.ID)
	assert.Greater(t, bundle.EstimatedTokens, 0)
}

// TestContext_Build_ExamplesTierToleratesUnwiredStore checks that wiring
// l.Examples() into the context builder's examples tier (PLAN §34b) does
// not break Build against a real Local engine: internal/example is not
// wired into Local yet (examples.go's exampleAPI reports E_NOT_IMPLEMENTED
// for every method), so the tier must degrade to empty rather than
// surfacing that error to the caller.
func TestContext_Build_ExamplesTierToleratesUnwiredStore(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	bundle, err := l.Context().Build(ctx, domain.ContextRequest{
		Intent:     "verify a rider is online",
		Operations: []string{"rider-service.getRider"},
	})
	require.NoError(t, err)
	require.NotNil(t, bundle)
	assert.Empty(t, bundle.Examples)
}

func TestContext_Build_ExplicitOperations(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	bundle, err := l.Context().Build(ctx, domain.ContextRequest{
		Intent:     "verify a rider is online",
		Operations: []string{"rider-service.getRider"},
	})
	require.NoError(t, err)
	require.Len(t, bundle.Operations, 1)
	assert.Equal(t, "rider-service.getRider", bundle.Operations[0].ID)
}
