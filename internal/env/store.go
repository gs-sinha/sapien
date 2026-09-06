// Package env resolves environments and secrets for outgoing requests: base
// URL and auth precedence (PLAN §7, §20), secret storage and ${secret.NAME}
// substitution, request auth application, and the production guard (PLAN
// §28).
package env

import (
	"path/filepath"
	"sort"
	"sync"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

// SecretStore stores and retrieves named secret values.
type SecretStore interface {
	// Get returns the named secret's value, or errs.SecretMissing if absent.
	Get(name string) (string, error)
	// Set stores a secret value.
	Set(name, value string) error
	// Delete removes a secret.
	Delete(name string) error
	// List returns the known secret names, sorted.
	List() ([]string, error)
}

// secretMissing builds the standard "secret not found" error used by every
// store and by SubstituteSecrets.
func secretMissing(name string) error {
	return errs.New(errs.SecretMissing, "secret %q not found", name).
		WithDetail("name", name).
		WithHint("run: sapien secret set NAME")
}

// readOnly builds the standard error returned by a read-only store's Set and
// Delete methods.
func readOnly(reason string) error {
	return errs.New(errs.Invalid, "read-only: %s", reason)
}

// --- Memory store ---------------------------------------------------------

type memoryStore struct {
	mu   sync.RWMutex
	data map[string]string
}

// NewMemoryStore returns a SecretStore backed by an in-process map. Useful
// for tests and for the CLI's non-interactive dry-run modes.
func NewMemoryStore() SecretStore {
	return &memoryStore{data: map[string]string{}}
}

func (m *memoryStore) Get(name string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[name]
	if !ok {
		return "", secretMissing(name)
	}
	return v, nil
}

func (m *memoryStore) Set(name, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[name] = value
	return nil
}

func (m *memoryStore) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, name)
	return nil
}

func (m *memoryStore) List() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.data))
	for k := range m.data {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}

// --- Chain -----------------------------------------------------------------

type chainStore struct {
	stores []SecretStore
}

// Chain combines stores with precedence: Get returns the value from the
// first store that has it; List returns the union of all names; Set and
// Delete are tried on each store in order, skipping any that reports itself
// read-only (errs.Invalid).
func Chain(stores ...SecretStore) SecretStore {
	return &chainStore{stores: stores}
}

func (c *chainStore) Get(name string) (string, error) {
	var lastErr error
	for _, s := range c.stores {
		v, err := s.Get(name)
		if err == nil {
			return v, nil
		}
		if errs.CodeOf(err) == errs.SecretMissing {
			lastErr = err
			continue
		}
		return "", err
	}
	if lastErr == nil {
		lastErr = secretMissing(name)
	}
	return "", lastErr
}

func (c *chainStore) Set(name, value string) error {
	var lastErr error
	for _, s := range c.stores {
		err := s.Set(name, value)
		if err == nil {
			return nil
		}
		if errs.CodeOf(err) == errs.Invalid {
			lastErr = err
			continue
		}
		return err
	}
	if lastErr == nil {
		lastErr = errs.New(errs.Invalid, "no writable secret store")
	}
	return lastErr
}

func (c *chainStore) Delete(name string) error {
	var lastErr error
	for _, s := range c.stores {
		err := s.Delete(name)
		if err == nil {
			return nil
		}
		if errs.CodeOf(err) == errs.Invalid {
			lastErr = err
			continue
		}
		return err
	}
	if lastErr == nil {
		lastErr = errs.New(errs.Invalid, "no writable secret store")
	}
	return lastErr
}

func (c *chainStore) List() ([]string, error) {
	set := map[string]struct{}{}
	for _, s := range c.stores {
		names, err := s.List()
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			set[n] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// DefaultChain returns the standard secret store for a workspace: process
// environment variables first (for CI), then the OS keychain, indexed at
// <workspace>/.sapien/secrets.index (PLAN §20).
func DefaultChain(ws *domain.Workspace) SecretStore {
	indexPath := filepath.Join(ws.Dir, domain.WorkspaceStateDir, "secrets.index")
	return Chain(NewEnvVarStore(), NewKeyringStore(ws.Name, indexPath))
}
