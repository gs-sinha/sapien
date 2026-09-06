package env

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/zalando/go-keyring"

	"github.com/gs-sinha/sapien/internal/errs"
)

// keyringService is the go-keyring "service" name every Sapien secret is
// stored under; the account name disambiguates by workspace and secret name.
const keyringService = "sapien"

// keyringBackend is the subset of github.com/zalando/go-keyring's API this
// package uses, factored out so tests can stub it without touching the real
// OS keychain.
type keyringBackend interface {
	Set(service, user, password string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

// systemKeyring is the production keyringBackend, delegating to the real OS
// keychain via go-keyring.
type systemKeyring struct{}

func (systemKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func (systemKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (systemKeyring) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

// keyringStore stores secret values in the OS keychain. Because keychains
// generally cannot enumerate their own entries, it also maintains a
// names-only index file (JSON array, 0600) alongside, updated on every Set
// and Delete; List reads that index rather than the keychain itself.
type keyringStore struct {
	workspaceName string
	indexPath     string
	backend       keyringBackend
	mu            sync.Mutex
}

// NewKeyringStore returns a SecretStore backed by the OS keychain (service
// "sapien", account "<workspaceName>/<name>"), with a names-only index kept
// at indexPath for List.
func NewKeyringStore(workspaceName, indexPath string) SecretStore {
	return newKeyringStore(workspaceName, indexPath, systemKeyring{})
}

// newKeyringStore is the same constructor with an injectable backend, used
// by tests to avoid touching the real OS keychain.
func newKeyringStore(workspaceName, indexPath string, backend keyringBackend) SecretStore {
	return &keyringStore{workspaceName: workspaceName, indexPath: indexPath, backend: backend}
}

func (k *keyringStore) account(name string) string {
	return k.workspaceName + "/" + name
}

func (k *keyringStore) Get(name string) (string, error) {
	v, err := k.backend.Get(keyringService, k.account(name))
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", secretMissing(name)
		}
		return "", errs.Wrap(errs.Internal, err, "reading secret %q from keychain", name)
	}
	return v, nil
}

func (k *keyringStore) Set(name, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.backend.Set(keyringService, k.account(name), value); err != nil {
		return errs.Wrap(errs.Internal, err, "writing secret %q to keychain", name)
	}
	return k.addToIndex(name)
}

func (k *keyringStore) Delete(name string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.backend.Delete(keyringService, k.account(name)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return errs.Wrap(errs.Internal, err, "deleting secret %q from keychain", name)
	}
	return k.removeFromIndex(name)
}

func (k *keyringStore) List() ([]string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	names, err := k.readIndex()
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

func (k *keyringStore) readIndex() ([]string, error) {
	data, err := os.ReadFile(k.indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errs.Wrap(errs.Internal, err, "reading secrets index %s", k.indexPath)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, errs.Wrap(errs.Internal, err, "parsing secrets index %s", k.indexPath)
	}
	return names, nil
}

func (k *keyringStore) writeIndex(names []string) error {
	sort.Strings(names)
	if names == nil {
		names = []string{}
	}
	data, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return errs.Wrap(errs.Internal, err, "encoding secrets index")
	}
	if dir := filepath.Dir(k.indexPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return errs.Wrap(errs.Internal, err, "creating secrets index directory %s", dir)
		}
	}
	if err := os.WriteFile(k.indexPath, data, 0o600); err != nil {
		return errs.Wrap(errs.Internal, err, "writing secrets index %s", k.indexPath)
	}
	return nil
}

func (k *keyringStore) addToIndex(name string) error {
	names, err := k.readIndex()
	if err != nil {
		return err
	}
	for _, n := range names {
		if n == name {
			return nil
		}
	}
	names = append(names, name)
	return k.writeIndex(names)
}

func (k *keyringStore) removeFromIndex(name string) error {
	names, err := k.readIndex()
	if err != nil {
		return err
	}
	out := names[:0]
	for _, n := range names {
		if n != name {
			out = append(out, n)
		}
	}
	return k.writeIndex(out)
}
