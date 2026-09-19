package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSemantic_EffectiveKindsAndEmbeds(t *testing.T) {
	assert.Equal(t, SemanticKinds, Semantic{}.EffectiveKinds(), "no list means every kind")

	s := Semantic{Kinds: []string{"memories", "bogus", "operations"}}
	assert.Equal(t, []string{"operations", "memories"}, s.EffectiveKinds(), "display order, unknown names dropped")
	assert.True(t, s.Embeds(SemanticKindOperations))
	assert.False(t, s.Embeds(SemanticKindDocs))
	assert.False(t, s.Embeds(SemanticKindExamples))
}

func TestWriteSemantic_KindsAndPrefixesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("SAPIEN_CONFIG", path)
	require.NoError(t, os.WriteFile(path, []byte("# mine\nmcp:\n  profile: default\n"), 0o600))

	kinds := []string{"operations", "examples"}
	empty := ""
	emptyPtr := &empty
	require.NoError(t, WriteSemantic(path, SemanticWrite{
		Enabled: true, Kind: "ollama", Model: "nomic-embed-text",
		Kinds: &kinds, DocumentPrefix: &emptyPtr,
	}))

	cfg, err := Load(nil)
	require.NoError(t, err)
	assert.Equal(t, kinds, cfg.Semantic.Kinds)
	assert.Nil(t, cfg.Semantic.QueryPrefix, "an untouched prefix stays unset: the model's default applies")
	require.NotNil(t, cfg.Semantic.DocumentPrefix, `an explicit "" is an override, not an absence`)
	assert.Equal(t, "", *cfg.Semantic.DocumentPrefix)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "profile: default", "unrelated keys survive")

	// Reset: both overrides go, and an empty kinds list removes the key.
	var none *string
	all := []string{}
	require.NoError(t, WriteSemantic(path, SemanticWrite{
		Enabled: true, Kind: "ollama", Model: "nomic-embed-text",
		Kinds: &all, QueryPrefix: &none, DocumentPrefix: &none,
	}))
	cfg, err = Load(nil)
	require.NoError(t, err)
	assert.Empty(t, cfg.Semantic.Kinds)
	assert.Nil(t, cfg.Semantic.DocumentPrefix)
	raw, _ = os.ReadFile(path)
	assert.NotContains(t, string(raw), "document_prefix")
	assert.NotContains(t, string(raw), "kinds")
}
