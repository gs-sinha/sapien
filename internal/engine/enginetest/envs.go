package enginetest

import (
	"context"
	"sort"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

func (e *envAPI) List(ctx context.Context) ([]domain.Environment, error) {
	f := e.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Envs.List", nil)

	out := make([]domain.Environment, 0, len(f.environments))
	for _, env := range f.environments {
		out = append(out, env)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (e *envAPI) Get(ctx context.Context, name string) (*domain.Environment, error) {
	f := e.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Envs.Get", name)

	env, ok := f.environments[name]
	if !ok {
		return nil, errs.New(errs.EnvNotFound, "environment %q not found", name).WithDetail("name", name)
	}
	cp := env
	return &cp, nil
}

func (e *envAPI) Default(ctx context.Context) (string, error) {
	f := e.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Envs.Default", nil)
	return f.defaultEnv, nil
}

func (e *envAPI) SetDefault(ctx context.Context, name string) error {
	f := e.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Envs.SetDefault", name)

	if _, ok := f.environments[name]; !ok {
		return errs.New(errs.EnvNotFound, "environment %q not found", name).WithDetail("name", name)
	}
	f.defaultEnv = name
	return nil
}

func (e *envAPI) SetSecret(ctx context.Context, name, value string) error {
	f := e.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Envs.SetSecret", name) // the value itself is never recorded
	f.secrets[name] = value
	return nil
}

func (e *envAPI) ListSecrets(ctx context.Context) ([]string, error) {
	f := e.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Envs.ListSecrets", nil)

	out := make([]string, 0, len(f.secrets))
	for name := range f.secrets {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func (e *envAPI) DeleteSecret(ctx context.Context, name string) error {
	f := e.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Envs.DeleteSecret", name)

	if _, ok := f.secrets[name]; !ok {
		return errs.New(errs.SecretMissing, "secret %q not found", name).WithDetail("name", name)
	}
	delete(f.secrets, name)
	return nil
}

var _ engine.EnvAPI = (*envAPI)(nil)
