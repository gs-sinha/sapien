package env

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/errs"
)

func TestMemoryStore(t *testing.T) {
	s := NewMemoryStore()

	_, err := s.Get("FOO")
	require.Error(t, err)
	assert.Equal(t, errs.SecretMissing, errs.CodeOf(err))
	assert.Equal(t, "FOO", errs.As(err).Details["name"])
	assert.NotEmpty(t, errs.As(err).Hint)

	require.NoError(t, s.Set("FOO", "bar"))
	v, err := s.Get("FOO")
	require.NoError(t, err)
	assert.Equal(t, "bar", v)

	require.NoError(t, s.Set("BAZ", "qux"))
	names, err := s.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"BAZ", "FOO"}, names)

	require.NoError(t, s.Delete("FOO"))
	_, err = s.Get("FOO")
	assert.True(t, errs.Is(err, errs.SecretMissing))
	names, err = s.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"BAZ"}, names)
}

func TestEnvVarStore_NameMapping(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"my-token", "SAPIEN_SECRET_MY_TOKEN"},
		{"MY_TOKEN", "SAPIEN_SECRET_MY_TOKEN"},
		{"api.key", "SAPIEN_SECRET_API_KEY"},
		{"simple", "SAPIEN_SECRET_SIMPLE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, envVarName(tt.name))
		})
	}
}

func TestEnvVarStore(t *testing.T) {
	t.Setenv("SAPIEN_SECRET_MY_TOKEN", "s3cr3t")
	s := NewEnvVarStore()

	v, err := s.Get("my-token")
	require.NoError(t, err)
	assert.Equal(t, "s3cr3t", v)

	_, err = s.Get("missing-token")
	require.Error(t, err)
	assert.True(t, errs.Is(err, errs.SecretMissing))

	names, err := s.List()
	require.NoError(t, err)
	assert.Contains(t, names, "MY_TOKEN")

	err = s.Set("my-token", "x")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))

	err = s.Delete("my-token")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestChain_GetPrecedence(t *testing.T) {
	first := NewMemoryStore()
	second := NewMemoryStore()
	require.NoError(t, second.Set("ONLY_SECOND", "second-value"))
	require.NoError(t, first.Set("SHARED", "first-value"))
	require.NoError(t, second.Set("SHARED", "second-shared-value"))

	c := Chain(first, second)

	v, err := c.Get("SHARED")
	require.NoError(t, err)
	assert.Equal(t, "first-value", v, "first store with the value wins")

	v, err = c.Get("ONLY_SECOND")
	require.NoError(t, err)
	assert.Equal(t, "second-value", v)

	_, err = c.Get("NOWHERE")
	assert.True(t, errs.Is(err, errs.SecretMissing))
}

func TestChain_List_Union(t *testing.T) {
	first := NewMemoryStore()
	second := NewMemoryStore()
	require.NoError(t, first.Set("A", "1"))
	require.NoError(t, second.Set("B", "2"))
	require.NoError(t, second.Set("A", "overlap"))

	c := Chain(first, second)
	names, err := c.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "B"}, names)
}

func TestChain_SetDelete_SkipsReadOnly(t *testing.T) {
	readonly := NewEnvVarStore() // Set/Delete always errs.Invalid
	writable := NewMemoryStore()

	c := Chain(readonly, writable)

	require.NoError(t, c.Set("NEW_SECRET", "value"))
	v, err := writable.Get("NEW_SECRET")
	require.NoError(t, err)
	assert.Equal(t, "value", v)

	require.NoError(t, c.Delete("NEW_SECRET"))
	_, err = writable.Get("NEW_SECRET")
	assert.True(t, errs.Is(err, errs.SecretMissing))
}

func TestChain_SetDelete_AllReadOnly(t *testing.T) {
	c := Chain(NewEnvVarStore(), NewEnvVarStore())

	err := c.Set("X", "y")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))

	err = c.Delete("X")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestChain_Set_StopsOnNonInvalidError(t *testing.T) {
	// A store that fails with a non-Invalid code should not be skipped.
	broken := failingStore{err: errs.New(errs.Internal, "disk full")}
	writable := NewMemoryStore()

	c := Chain(broken, writable)
	err := c.Set("X", "y")
	require.Error(t, err)
	assert.Equal(t, errs.Internal, errs.CodeOf(err))

	_, getErr := writable.Get("X")
	assert.True(t, errs.Is(getErr, errs.SecretMissing), "writable store must not have been reached")
}

// failingStore is a SecretStore whose Set/Delete always fail with a fixed
// error, used to verify Chain does not skip past non-read-only failures.
type failingStore struct {
	err error
}

func (f failingStore) Get(name string) (string, error) { return "", f.err }
func (f failingStore) Set(name, value string) error    { return f.err }
func (f failingStore) Delete(name string) error        { return f.err }
func (f failingStore) List() ([]string, error)         { return nil, f.err }

func TestDefaultChain_OrdersEnvVarBeforeKeyring(t *testing.T) {
	// DefaultChain must be constructible without touching the real
	// keychain at call time (the keyring backend is only invoked on
	// Get/Set/Delete/List, not at construction).
	ws := testWorkspace(t)
	store := DefaultChain(ws)
	require.NotNil(t, store)

	// A name only visible via env var should resolve through the chain.
	t.Setenv("SAPIEN_SECRET_ONLY_ENV", "env-value")
	v, err := store.Get("only-env")
	require.NoError(t, err)
	assert.Equal(t, "env-value", v)
}
