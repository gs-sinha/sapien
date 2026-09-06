package cli_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupRealFixtureWorkspace inits a workspace and registers the three
// logistics fixture services against the real engine (not enginetest.Fake),
// exactly as TestPhase1_EndToEnd does, so `docs`/`describe`/`search` exercise
// real OpenAPI/Markdown ingestion: security, deprecated, examples, docs
// sections, and multiple docs per/across services.
func setupRealFixtureWorkspace(t *testing.T) (wsDir string) {
	t.Helper()
	wsDir = t.TempDir()
	fixDir := copyFixtures(t)

	_, stderr, code := run(t, "init", wsDir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	for _, name := range []string{"order-service", "allocation-service", "rider-service"} {
		_, stderr, code := run(t, "--workspace", wsDir, "service", "add", filepath.Join(fixDir, name))
		require.Equalf(t, 0, code, "adding %s: stderr: %s", name, stderr)
	}
	return wsDir
}

// --- docs list ---

func TestDocsList_Human(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "docs", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "SERVICE")
	assert.Contains(t, stdout, "orders.md")
	assert.Contains(t, stdout, "timeline.md")
	assert.Contains(t, stdout, "riders.md")
	assert.Contains(t, stdout, "allocation.md")
}

func TestDocsList_ServiceFilter(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "docs", "list", "--service", "order-service", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var docs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &docs))
	require.NotEmpty(t, docs)
	for _, d := range docs {
		assert.Equal(t, "order-service", d["service_id"])
	}
}

// --- docs search ---

func TestDocsSearch_Human(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "docs", "search", "rider")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "SCORE")
	assert.Contains(t, stdout, "HEADING")
}

func TestDocsSearch_ServiceFilter_JSON(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "docs", "search", "allocation", "--service", "allocation-service", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var results []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &results))
	require.NotEmpty(t, results)
	for _, r := range results {
		assert.Equal(t, "allocation-service", r["service"])
	}
}

// --- docs show / --section ---

func TestDocsShow_Full_Human(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "docs", "show", "allocation-service", "docs/allocation.md")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "QCOM allocation rules")
	assert.Contains(t, stdout, "Deprecated endpoint")
}

func TestDocsShow_Section_ExactHeading(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "docs", "show", "allocation-service", "docs/allocation.md",
		"--section", "QCOM allocation rules")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "## QCOM allocation rules")
	assert.Contains(t, stdout, "STANDARD")
	assert.NotContains(t, stdout, "Deprecated endpoint")
}

func TestDocsShow_Section_BySlug(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "docs", "show", "allocation-service", "docs/allocation.md",
		"--section", "qcom-allocation-rules", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	sections, ok := got["sections"].([]any)
	require.True(t, ok)
	require.Len(t, sections, 1)
	sec := sections[0].(map[string]any)
	assert.Equal(t, "QCOM allocation rules", sec["heading"])
}

func TestDocsShow_Section_NotFound(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	_, stderr, code := run(t, "--workspace", wsDir, "docs", "show", "allocation-service", "docs/allocation.md",
		"--section", "no such section", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
	assert.NotEmpty(t, got["hint"])
}

func TestDocsShow_DocNotFound(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	_, stderr, code := run(t, "--workspace", wsDir, "docs", "show", "order-service", "docs/no-such.md", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_DOC_NOT_FOUND", got["code"])
}

// --- search --docs ---

func TestSearch_Docs_Human(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "search", "qcomSkill", "--docs")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "SCORE")
	assert.Contains(t, stdout, "SNIPPET")
}

func TestSearch_Docs_JSON(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "search", "delivery timeline", "--docs", "--service", "order-service", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var results []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &results))
	for _, r := range results {
		assert.Equal(t, "order-service", r["service"])
	}
}

// --- search (operations): human mode + method/limit filters, never
// exercised elsewhere ---

func TestSearch_Operations_Human(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "search", "order")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "SCORE")
	assert.Contains(t, stdout, "METHOD")
}

func TestSearch_Operations_MethodAndLimit(t *testing.T) {
	wsDir := setupRealFixtureWorkspace(t)
	stdout, stderr, code := run(t, "--workspace", wsDir, "search", "order", "--method", "POST", "--limit", "1", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var results []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &results))
	require.LessOrEqual(t, len(results), 1)
	for _, r := range results {
		op := r["operation"].(map[string]any)
		http := op["http"].(map[string]any)
		assert.Equal(t, "POST", http["method"])
	}
}
