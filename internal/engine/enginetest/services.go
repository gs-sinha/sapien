package enginetest

import (
	"context"
	"sort"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

func (s *serviceAPI) List(ctx context.Context) ([]domain.Service, error) {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Services.List", nil)

	out := make([]domain.Service, 0, len(f.services))
	for _, svc := range f.services {
		out = append(out, svc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *serviceAPI) Get(ctx context.Context, name string) (*domain.Service, error) {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Services.Get", name)

	svc, ok := f.services[name]
	if !ok {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}
	cp := svc
	return &cp, nil
}

func (s *serviceAPI) Add(ctx context.Context, name string, src domain.Source) (*domain.Service, error) {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Services.Add", map[string]any{"name": name, "source": src})

	if _, exists := f.services[name]; exists {
		return nil, errs.New(errs.Conflict, "service %q already exists", name).WithDetail("name", name)
	}
	svc := domain.Service{
		ID:         name,
		Name:       name,
		Source:     src,
		PackageDir: src.Path,
		Status:     domain.SyncOK,
	}
	f.services[name] = svc
	cp := svc
	return &cp, nil
}

func (s *serviceAPI) Remove(ctx context.Context, name string) error {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Services.Remove", name)

	if _, ok := f.services[name]; !ok {
		return errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}
	delete(f.services, name)
	return nil
}

func (s *serviceAPI) Sync(ctx context.Context, name string) ([]domain.Service, error) {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Services.Sync", name)

	if name == "" {
		out := make([]domain.Service, 0, len(f.services))
		for _, svc := range f.services {
			out = append(out, svc)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out, nil
	}

	svc, ok := f.services[name]
	if !ok {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}
	return []domain.Service{svc}, nil
}

func (s *serviceAPI) Reindex(ctx context.Context) error {
	f := s.f()
	f.mu.Lock()
	f.reindexedServices = true
	f.recordLocked("Services.Reindex", nil)
	f.mu.Unlock()

	f.Publish(domain.Event{Type: domain.EventCatalogChanged, Time: time.Now().UTC()})
	return nil
}

var _ engine.ServiceAPI = (*serviceAPI)(nil)
