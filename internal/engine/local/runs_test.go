package local

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

func TestRuns_PinAndPurge(t *testing.T) {
	me := setupEngineWithMock(t)
	l := me.l
	ctx := context.Background()

	run1, err := l.Runner().Call(ctx, engine.CallRequest{
		Operation: "rider-service.getRider",
		Params:    map[string]any{"riderId": "R123"},
		Env:       "test",
	})
	require.NoError(t, err)
	run2, err := l.Runner().Call(ctx, engine.CallRequest{
		Operation: "rider-service.getRider",
		Params:    map[string]any{"riderId": "R124"},
		Env:       "test",
	})
	require.NoError(t, err)

	require.NoError(t, l.Runs().Pin(ctx, run1.ID, true))
	got, err := l.Runs().Get(ctx, run1.ID)
	require.NoError(t, err)
	assert.True(t, got.Pinned)

	list, err := l.Runs().List(ctx, domain.RunFilter{})
	require.NoError(t, err)
	assert.Len(t, list, 2)

	// keep<=0 disables the count-based rule and neither run is 30 days old,
	// so nothing is purged.
	deleted, err := l.Runs().Purge(ctx, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)

	_, err = l.Runs().Get(ctx, run1.ID)
	require.NoError(t, err)
	_, err = l.Runs().Get(ctx, run2.ID)
	require.NoError(t, err)
}
