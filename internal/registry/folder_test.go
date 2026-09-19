package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scanFlows discovers a service-tier flow anywhere under flows/, at any
// depth (PLAN §34f item 6), the same as internal/engine/local's
// reindexOwnerFlows -- so a flow a teammate committed into a subfolder of a
// git-sourced service's api/flows is found on sync_service, not only after
// a write through the engine.
func TestScanFlows_FindsFlowInNestedFolder(t *testing.T) {
	dir := t.TempDir()
	flowsDir := filepath.Join(dir, "flows")
	nested := filepath.Join(flowsDir, "smoke", "checkout")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	content := "version: 1\nid: nested-smoke\nname: Nested smoke test\nsteps:\n  - id: a\n    call: order-service.createOrder\n"
	require.NoError(t, os.WriteFile(filepath.Join(nested, "nested-smoke.flow.yaml"), []byte(content), 0o644))
	// A file directly at the root of flows/ must still be found too.
	rootContent := "version: 1\nid: root-smoke\nsteps:\n  - id: a\n    call: order-service.createOrder\n"
	require.NoError(t, os.WriteFile(filepath.Join(flowsDir, "root-smoke.flow.yaml"), []byte(rootContent), 0o644))

	pkg := &Package{Dir: dir, FlowsDir: flowsDir}
	out, err := scanFlows("order-service", pkg)
	require.NoError(t, err)
	require.Len(t, out, 2)

	byID := map[string]int{}
	for i, f := range out {
		byID[f.ID] = i
	}
	nestedFlow := out[byID["nested-smoke"]]
	assert.Equal(t, "smoke/checkout", nestedFlow.Folder)
	assert.Equal(t, filepath.ToSlash(filepath.Join("flows", "smoke", "checkout", "nested-smoke.flow.yaml")), nestedFlow.Path)

	rootFlow := out[byID["root-smoke"]]
	assert.Equal(t, "", rootFlow.Folder)
}

// A directory whose name starts with "." under flows/ is skipped, mirroring
// the file watcher and internal/memory/internal/example's locators.
func TestScanFlows_SkipsDotDirectories(t *testing.T) {
	dir := t.TempDir()
	flowsDir := filepath.Join(dir, "flows")
	hidden := filepath.Join(flowsDir, ".git")
	require.NoError(t, os.MkdirAll(hidden, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hidden, "ignored.flow.yaml"), []byte("version: 1\nid: ignored\nsteps: []\n"), 0o644))

	pkg := &Package{Dir: dir, FlowsDir: flowsDir}
	out, err := scanFlows("order-service", pkg)
	require.NoError(t, err)
	assert.Empty(t, out)
}
