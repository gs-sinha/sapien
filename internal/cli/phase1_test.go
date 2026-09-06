package cli_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixturesRoot returns the absolute path of fixtures/logistics, resolved
// relative to this test file rather than the working directory `go test`
// happens to use.
func fixturesRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(file), "..", "..", "fixtures", "logistics")
}

// copyFixtures copies the three logistics service packages into a fresh
// temp dir and returns it, so these tests never write into
// fixtures/logistics itself.
func copyFixtures(t *testing.T) (dir string) {
	t.Helper()
	root := fixturesRoot(t)
	dir = t.TempDir()
	for _, svc := range []string{"order-service", "allocation-service", "rider-service"} {
		require.NoError(t, copyDir(filepath.Join(root, svc), filepath.Join(dir, svc)))
	}
	return dir
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// TestPhase1_EndToEnd walks the Phase 1 exit criterion end to end, entirely
// in-process via cli.Execute: init a workspace, register the three
// logistics services, list/search/describe/show-docs, and reindex.
func TestPhase1_EndToEnd(t *testing.T) {
	wsDir := t.TempDir()
	fixDir := copyFixtures(t)

	stdout, stderr, code := run(t, "init", wsDir, "--name", "logistics")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	require.Contains(t, stdout, "logistics")

	// service add x3
	for _, name := range []string{"order-service", "allocation-service", "rider-service"} {
		stdout, stderr, code := run(t, "--workspace", wsDir, "service", "add", filepath.Join(fixDir, name))
		require.Equalf(t, 0, code, "adding %s: stderr: %s", name, stderr)
		require.Contains(t, stdout, name)
		if name == "allocation-service" {
			assert.Contains(t, stdout, "SYNTHESIZED_OPERATION_ID",
				"allocation-service's GET /v1/allocations/stats has no operationId and should warn")
		}
	}

	// service list --json
	stdout, stderr, code = run(t, "--workspace", wsDir, "service", "list", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var services []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &services))
	require.Len(t, services, 3)
	for _, s := range services {
		assert.Equal(t, "ok", s["status"])
		assert.Greater(t, s["operation_count"], float64(0))
	}

	// search "allocate rider" --json
	stdout, stderr, code = run(t, "--workspace", wsDir, "search", "allocate rider", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var searchResults []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &searchResults))
	require.NotEmpty(t, searchResults)
	firstOp, ok := searchResults[0]["operation"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "allocation-service.allocate", firstOp["id"])

	// describe allocation-service.allocate --json
	stdout, stderr, code = run(t, "--workspace", wsDir, "describe", "allocation-service.allocate", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var described struct {
		Operation map[string]any   `json:"operation"`
		Fields    []map[string]any `json:"fields"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &described))
	assert.Equal(t, "allocation-service.allocate", described.Operation["id"])
	var foundOrderID bool
	for _, f := range described.Fields {
		if path, _ := f["path"].(string); path == "request.body.orderId" {
			foundOrderID = true
		}
	}
	assert.True(t, foundOrderID, "expected request.body.orderId among allocate's fields, got %+v", described.Fields)

	// docs show allocation-service docs/allocation.md
	stdout, stderr, code = run(t, "--workspace", wsDir, "docs", "show", "allocation-service", "docs/allocation.md")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "QCOM")

	// reindex
	stdout, stderr, code = run(t, "--workspace", wsDir, "reindex", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "\"stats\"")
}

func TestDescribe_UnknownOperation(t *testing.T) {
	wsDir := t.TempDir()
	_, _, code := run(t, "init", wsDir)
	require.Equal(t, 0, code)

	stdout, stderr, code := run(t, "--workspace", wsDir, "describe", "no-such-service.noOp", "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_OPERATION_NOT_FOUND", got["code"])
}

func TestService_NoWorkspace(t *testing.T) {
	dir := t.TempDir() // empty, no workspace anywhere above it in the temp tree
	stdout, stderr, code := run(t, "--workspace", dir, "service", "list", "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_WORKSPACE_NOT_FOUND", got["code"])
}
