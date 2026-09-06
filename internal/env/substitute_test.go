package env

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/errs"
)

func newTestStore(t *testing.T, values map[string]string) SecretStore {
	t.Helper()
	s := NewMemoryStore()
	for k, v := range values {
		require.NoError(t, s.Set(k, v))
	}
	return s
}

func TestHasSecretRef(t *testing.T) {
	assert.True(t, HasSecretRef("Bearer ${secret.TOKEN}"))
	assert.False(t, HasSecretRef("Bearer plain-token"))
	assert.False(t, HasSecretRef("${not.a.secret}"))
}

func TestSecretNames(t *testing.T) {
	assert.Nil(t, SecretNames("no refs here"))
	assert.Equal(t, []string{"TOKEN"}, SecretNames("Bearer ${secret.TOKEN}"))
	assert.Equal(t, []string{"A", "B"}, SecretNames("${secret.A}-${secret.B}"))
	// Repeated references are deduped, order of first appearance preserved.
	assert.Equal(t, []string{"A", "B"}, SecretNames("${secret.A}-${secret.B}-${secret.A}"))
}

func TestSubstituteSecrets_NoRefs(t *testing.T) {
	store := newTestStore(t, nil)
	out, used, err := SubstituteSecrets("plain string", store)
	require.NoError(t, err)
	assert.Equal(t, "plain string", out)
	assert.Nil(t, used)
}

func TestSubstituteSecrets_Single(t *testing.T) {
	store := newTestStore(t, map[string]string{"TOKEN": "abc123"})
	out, used, err := SubstituteSecrets("Bearer ${secret.TOKEN}", store)
	require.NoError(t, err)
	assert.Equal(t, "Bearer abc123", out)
	assert.Equal(t, []string{"TOKEN"}, used)
}

func TestSubstituteSecrets_Multiple(t *testing.T) {
	store := newTestStore(t, map[string]string{"USER": "alice", "PASS": "hunter2"})
	out, used, err := SubstituteSecrets("${secret.USER}:${secret.PASS}", store)
	require.NoError(t, err)
	assert.Equal(t, "alice:hunter2", out)
	assert.ElementsMatch(t, []string{"USER", "PASS"}, used)
}

func TestSubstituteSecrets_RepeatedRefDedupedInUsed(t *testing.T) {
	store := newTestStore(t, map[string]string{"TOKEN": "abc"})
	out, used, err := SubstituteSecrets("${secret.TOKEN}-${secret.TOKEN}", store)
	require.NoError(t, err)
	assert.Equal(t, "abc-abc", out)
	assert.Equal(t, []string{"TOKEN"}, used)
}

func TestSubstituteSecrets_Missing(t *testing.T) {
	store := newTestStore(t, nil)
	out, used, err := SubstituteSecrets("Bearer ${secret.MISSING}", store)
	require.Error(t, err)
	assert.Equal(t, "", out)
	assert.Nil(t, used)

	assert.Equal(t, errs.SecretMissing, errs.CodeOf(err))
	e := errs.As(err)
	assert.Equal(t, "MISSING", e.Details["name"])
	assert.NotEmpty(t, e.Hint)
}
