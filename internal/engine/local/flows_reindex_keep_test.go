package local

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const keepFlowYAML = `version: 1
id: keep-me
steps:
  - id: create
    call: order-service.createOrder
    body: { customerId: c1, type: STANDARD }
`

// A flow whose file stops parsing (an editor mid-save, a syntax slip, an
// older grammar) keeps its index row instead of vanishing from list/get.
func TestReindexWorkspaceFlows_KeepsRowWhenFileFailsToParse(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Flows().Create(ctx, keepFlowYAML, "")
	require.NoError(t, err)
	path := created.Path

	require.NoError(t, os.WriteFile(path, []byte("version: 1\nid: keep-me\nsteps: [ this is not: valid yaml\n"), 0o644))
	require.NoError(t, l.reindexWorkspaceFlows(ctx))
	// The row survives: the flow is still listed, and asking for it by id
	// fails with the parse error rather than "not found".
	list, err := l.Flows().List(ctx, "")
	require.NoError(t, err)
	found := false
	for _, f := range list {
		if f.ID == "keep-me" {
			found = true
		}
	}
	assert.True(t, found, "flow stays listed while its file is broken")
	_, err = l.Flows().Get(ctx, "keep-me")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "not found")

	require.NoError(t, os.WriteFile(path, []byte(keepFlowYAML), 0o644))
	require.NoError(t, l.reindexWorkspaceFlows(ctx))
	got, err := l.Flows().Get(ctx, "keep-me")
	require.NoError(t, err)
	assert.Len(t, got.Steps, 1)

	// A deleted file, by contrast, does drop the row.
	require.NoError(t, os.Remove(path))
	require.NoError(t, l.reindexWorkspaceFlows(ctx))
	_, err = l.Flows().Get(ctx, "keep-me")
	assert.Error(t, err)
}
