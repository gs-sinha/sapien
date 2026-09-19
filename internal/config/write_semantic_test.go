package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
)

func strPtr(s string) *string { return &s }

func TestWriteSemantic_CreatesFileAndParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", ".sapien", "config.yaml")

	require.NoError(t, config.WriteSemantic(path, config.SemanticWrite{
		Enabled: true, Kind: "ollama", BaseURL: "http://127.0.0.1:11434",
		Model: "nomic-embed-text", BatchSize: 8, APIKey: strPtr("secret"),
	}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "semantic:")
	assert.Contains(t, string(data), "nomic-embed-text")

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the file may hold an api_key")
	}

	ws := &domain.Workspace{Dir: dir, File: filepath.Join(dir, domain.WorkspaceFileName)}
	t.Setenv("SAPIEN_CONFIG", path)
	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.True(t, cfg.Semantic.Enabled)
	assert.Equal(t, "ollama", cfg.Semantic.Kind)
	assert.Equal(t, "http://127.0.0.1:11434", cfg.Semantic.BaseURL)
	assert.Equal(t, "nomic-embed-text", cfg.Semantic.Model)
	assert.Equal(t, 8, cfg.Semantic.BatchSize)
	assert.Equal(t, "secret", cfg.Semantic.APIKey)
	_ = ws
}

// TestWriteSemantic_PreservesUnrelatedKeysAndComments proves the round-trip
// through yaml.Node leaves other top-level blocks (and a comment between
// them) alone.
func TestWriteSemantic_PreservesUnrelatedKeysAndComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "git:\n  cache_dir: /custom/repos\n# a comment between blocks\ndaemon:\n  idle_timeout: 45m\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

	require.NoError(t, config.WriteSemantic(path, config.SemanticWrite{
		Enabled: true, Kind: "openai", BaseURL: "https://api.openai.com/v1",
		Model: "text-embedding-3-small", BatchSize: 32,
	}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	out := string(data)
	assert.Contains(t, out, "/custom/repos")
	assert.Contains(t, out, "45m")
	assert.Contains(t, out, "a comment between blocks")
	assert.Contains(t, out, "text-embedding-3-small")
}

// TestWriteSemantic_APIKeyTriState proves nil keeps, "" clears, and a
// non-empty pointer sets the stored api_key -- exactly PUT /v1/settings/
// semantic's contract for api_key.
func TestWriteSemantic_APIKeyTriState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	t.Setenv("SAPIEN_CONFIG", path)

	require.NoError(t, config.WriteSemantic(path, config.SemanticWrite{Kind: "ollama", APIKey: strPtr("first-key")}))
	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, "first-key", cfg.Semantic.APIKey)

	// nil: keep the stored key.
	require.NoError(t, config.WriteSemantic(path, config.SemanticWrite{Kind: "ollama", Model: "m"}))
	cfg, err = config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, "first-key", cfg.Semantic.APIKey, "a nil APIKey must not touch the stored key")

	// "": clear it.
	require.NoError(t, config.WriteSemantic(path, config.SemanticWrite{Kind: "ollama", Model: "m", APIKey: strPtr("")}))
	cfg, err = config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Semantic.APIKey)
}

// TestWriteSemantic_EnvReferenceSurvivesVerbatim proves a "${env.NAME}"
// value round-trips unresolved -- config.Load never resolves it either;
// only internal/semantic.NewHTTPEmbedder does, at use time.
func TestWriteSemantic_EnvReferenceSurvivesVerbatim(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	t.Setenv("SAPIEN_CONFIG", path)

	require.NoError(t, config.WriteSemantic(path, config.SemanticWrite{
		Kind: "openai", Model: "m", APIKey: strPtr("${env.OPENAI_API_KEY}"),
	}))
	cfg, err := config.Load(nil)
	require.NoError(t, err)
	assert.Equal(t, "${env.OPENAI_API_KEY}", cfg.Semantic.APIKey)
}

func TestSemanticSource(t *testing.T) {
	userDir := t.TempDir()
	userPath := filepath.Join(userDir, "config.yaml")
	t.Setenv("SAPIEN_CONFIG", userPath)

	ws := &domain.Workspace{Dir: t.TempDir(), File: filepath.Join(t.TempDir(), domain.WorkspaceFileName)}

	source, err := config.SemanticSource(ws)
	require.NoError(t, err)
	assert.Equal(t, "default", source)

	require.NoError(t, config.WriteSemantic(userPath, config.SemanticWrite{Kind: "ollama", Model: "m"}))
	source, err = config.SemanticSource(ws)
	require.NoError(t, err)
	assert.Equal(t, "user", source)

	wsConfigPath := config.WorkspacePath(ws)
	require.NoError(t, os.MkdirAll(filepath.Dir(wsConfigPath), 0o755))
	require.NoError(t, config.WriteSemantic(wsConfigPath, config.SemanticWrite{Kind: "openai", Model: "m2"}))
	source, err = config.SemanticSource(ws)
	require.NoError(t, err)
	assert.Equal(t, "workspace", source)

	// A nil workspace only ever considers the user file.
	source, err = config.SemanticSource(nil)
	require.NoError(t, err)
	assert.Equal(t, "user", source)
}
