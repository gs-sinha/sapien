package spec

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

// TestNormalizeYAML_DefensiveMapAnyAny covers the map[any]any branch of
// normalizeYAML. gopkg.in/yaml.v3 does not produce this shape when
// unmarshalling into `any` (it gives map[string]any, unlike yaml.v2), but
// the branch guards against that changing.
func TestNormalizeYAML_DefensiveMapAnyAny(t *testing.T) {
	in := map[any]any{"a": 1, 2: "b"}
	out := normalizeYAML(in)
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", out)
	}
	assert.Equal(t, 1, m["a"])
	assert.Equal(t, "b", m["2"])
}

func TestPathString(t *testing.T) {
	cases := []struct {
		tokens []string
		want   string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"steps"}, "steps"},
		{[]string{"steps", "1", "call"}, "steps[1].call"},
		{[]string{"a", "b"}, "a.b"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, pathString(c.tokens))
	}
}

func TestIsIndexToken(t *testing.T) {
	assert.True(t, isIndexToken("0"))
	assert.True(t, isIndexToken("42"))
	assert.False(t, isIndexToken(""))
	assert.False(t, isIndexToken("a1"))
	assert.False(t, isIndexToken("-1"))
}

func TestLineForTokens_EdgeCases(t *testing.T) {
	// nil / empty document.
	var empty yaml.Node
	assert.Equal(t, 0, lineForTokens(&empty, []string{"a"}))

	var root yaml.Node
	require := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	require(yaml.Unmarshal([]byte("a:\n  b: 1\nlist:\n  - x\n  - y\n"), &root))

	// Fully resolvable path.
	assert.Greater(t, lineForTokens(&root, []string{"a", "b"}), 0)

	// Sequence index.
	assert.Greater(t, lineForTokens(&root, []string{"list", "1"}), 0)

	// Unresolvable tail falls back to the deepest reached node's line, not 0.
	assert.Greater(t, lineForTokens(&root, []string{"a", "missing", "deeper"}), 0)

	// Out-of-range sequence index.
	assert.Greater(t, lineForTokens(&root, []string{"list", "99"}), 0)

	// Root-level (empty tokens).
	assert.Greater(t, lineForTokens(&root, nil), 0)

	// Non-index token against a sequence node, and non-existent key against
	// a mapping node.
	assert.Greater(t, lineForTokens(&root, []string{"list", "notanindex"}), 0)
}

func TestRatStr(t *testing.T) {
	assert.Equal(t, "?", ratStr(nil))
	assert.Equal(t, "2", ratStr(big.NewRat(2, 1)))
	assert.Equal(t, "0.5", ratStr(big.NewRat(1, 2)))
}

func TestPluralAndQuoteHelpers(t *testing.T) {
	assert.Equal(t, "property", plural(1, "property", "properties"))
	assert.Equal(t, "properties", plural(0, "property", "properties"))
	assert.Equal(t, "properties", plural(2, "property", "properties"))
	assert.Equal(t, `"a", "b"`, quoteJoin([]string{"a", "b"}))
	assert.Equal(t, "1, true", joinAny([]any{1, true}))
}
