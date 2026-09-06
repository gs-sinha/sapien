package env

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/errs"
)

// fakeKeyringBackend is an in-memory stand-in for the OS keychain, so tests
// never touch the real one.
type fakeKeyringBackend struct {
	mu   sync.Mutex
	data map[string]string
}

func newFakeKeyringBackend() *fakeKeyringBackend {
	return &fakeKeyringBackend{data: map[string]string{}}
}

func (f *fakeKeyringBackend) key(service, user string) string { return service + "\x00" + user }

func (f *fakeKeyringBackend) Set(service, user, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[f.key(service, user)] = password
	return nil
}

func (f *fakeKeyringBackend) Get(service, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.data[f.key(service, user)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeKeyringBackend) Delete(service, user string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := f.key(service, user)
	if _, ok := f.data[k]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.data, k)
	return nil
}

func TestKeyringStore_AccountNameMapping(t *testing.T) {
	backend := newFakeKeyringBackend()
	indexPath := filepath.Join(t.TempDir(), "secrets.index")
	s := newKeyringStore("logistics", indexPath, backend)

	require.NoError(t, s.Set("STAGING_TOKEN", "s3cr3t"))

	// The value must land under service "sapien", account
	// "<workspaceName>/<NAME>" exactly.
	v, err := backend.Get("sapien", "logistics/STAGING_TOKEN")
	require.NoError(t, err)
	assert.Equal(t, "s3cr3t", v)

	// A different workspace name must not collide.
	other := newKeyringStore("other-ws", indexPath, backend)
	_, err = other.Get("STAGING_TOKEN")
	assert.True(t, errs.Is(err, errs.SecretMissing))
}

func TestKeyringStore_GetMissing(t *testing.T) {
	backend := newFakeKeyringBackend()
	s := newKeyringStore("ws", filepath.Join(t.TempDir(), "secrets.index"), backend)

	_, err := s.Get("NOPE")
	require.Error(t, err)
	assert.True(t, errs.Is(err, errs.SecretMissing))
	assert.Equal(t, "NOPE", errs.As(err).Details["name"])
}

func TestKeyringStore_IndexRoundTrip(t *testing.T) {
	backend := newFakeKeyringBackend()
	indexPath := filepath.Join(t.TempDir(), "nested", "secrets.index")
	s := newKeyringStore("ws", indexPath, backend)

	names, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, names)

	require.NoError(t, s.Set("B_TOKEN", "1"))
	require.NoError(t, s.Set("A_TOKEN", "2"))
	require.NoError(t, s.Set("A_TOKEN", "2-updated")) // re-set must not duplicate the index entry

	names, err = s.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"A_TOKEN", "B_TOKEN"}, names)

	v, err := s.Get("A_TOKEN")
	require.NoError(t, err)
	assert.Equal(t, "2-updated", v)

	// A fresh store instance pointed at the same index file must see the
	// same names, proving the index actually persisted to disk.
	reopened := newKeyringStore("ws", indexPath, backend)
	names, err = reopened.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"A_TOKEN", "B_TOKEN"}, names)

	require.NoError(t, s.Delete("A_TOKEN"))
	names, err = s.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"B_TOKEN"}, names)

	_, err = s.Get("A_TOKEN")
	assert.True(t, errs.Is(err, errs.SecretMissing))

	// Deleting an already-absent name is a no-op, not an error, and leaves
	// the index untouched.
	require.NoError(t, s.Delete("A_TOKEN"))
	names, err = s.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"B_TOKEN"}, names)
}

func TestKeyringStore_IndexFilePermissions(t *testing.T) {
	backend := newFakeKeyringBackend()
	indexPath := filepath.Join(t.TempDir(), "secrets.index")
	s := newKeyringStore("ws", indexPath, backend)
	require.NoError(t, s.Set("TOKEN", "v"))

	info, err := os.Stat(indexPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
