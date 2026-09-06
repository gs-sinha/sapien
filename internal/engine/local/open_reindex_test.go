package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/example"
	"github.com/growsimplee/sapien/internal/workspace"
)

// A memory or example committed inside a service repo must be visible to a
// one-shot CLI Open on a machine that has never indexed it (fresh clone,
// deleted .sapien/): the stores read <service>/api/{memories,examples}/ and
// the service directories have to be known before that reindex runs.
func TestOpen_FreshIndex_FindsServiceScopedMemoriesAndExamples(t *testing.T) {
	ws, _ := setupWorkspace(t)
	ctx := context.Background()

	l1, err := Open(ws, Options{})
	require.NoError(t, err)

	svc, err := l1.Services().Get(ctx, "order-service")
	require.NoError(t, err)
	require.NotEmpty(t, svc.PackageDir)

	mem, err := l1.Memories().Create(ctx, domain.Memory{
		Text:    "createOrder rejects a QCOM order without a pickup pincode.",
		Scope:   domain.ScopeService,
		Subject: domain.Subject{Service: "order-service"},
	})
	require.NoError(t, err)
	assert.True(t, filepath.HasPrefix(mem.FilePath, filepath.Join(svc.PackageDir, domain.MemoriesDir)), "memory file %q should live under the service package", mem.FilePath)

	// Drop an example file straight into the service's api/examples/, as a
	// teammate's commit would.
	exDir := filepath.Join(svc.PackageDir, example.ExamplesDir)
	require.NoError(t, os.MkdirAll(exDir, 0o755))
	data, err := example.Marshal(&domain.SavedExample{
		Version: 1, ID: "committed-order", Operation: "order-service.createOrder",
		Body: map[string]any{"customerId": "c1", "type": "STANDARD"},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(exDir, example.FileName("committed-order")), data, 0o644))

	require.NoError(t, l1.Close())

	// Simulate a fresh machine: no index at all.
	require.NoError(t, os.Remove(workspace.DBPath(ws)))

	l2, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l2.Close()

	got, err := l2.Memories().Get(ctx, mem.ID)
	require.NoError(t, err, "service-scoped memory must be indexed by a fresh Open")
	assert.Equal(t, domain.ScopeService, got.Scope)

	ex, err := l2.Examples().Get(ctx, "committed-order")
	require.NoError(t, err, "service-scoped example must be indexed by a fresh Open")
	assert.Equal(t, domain.ExampleScopeService, ex.Scope)
	assert.Equal(t, "order-service", ex.Service)
}

// A second Open with nothing changed must not reindex (the fingerprints
// are stored), and a new workspace-level example appearing must be picked up.
func TestOpen_ReindexesExamplesWhenWorkspaceDirChanges(t *testing.T) {
	ws, _ := setupWorkspace(t)
	ctx := context.Background()

	l1, err := Open(ws, Options{})
	require.NoError(t, err)
	require.NoError(t, l1.Close())

	exDir := filepath.Join(ws.Dir, example.ExamplesDir)
	require.NoError(t, os.MkdirAll(exDir, 0o755))
	data, err := example.Marshal(&domain.SavedExample{
		Version: 1, ID: "ws-order", Operation: "order-service.createOrder",
		Body: map[string]any{"customerId": "c2", "type": "QCOM"},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(exDir, example.FileName("ws-order")), data, 0o644))

	l2, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l2.Close()
	ex, err := l2.Examples().Get(ctx, "ws-order")
	require.NoError(t, err)
	assert.Equal(t, domain.ExampleScopeWorkspace, ex.Scope)
}
