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

// Bind records an override on the service: the fake has no filesystem, so
// it marks the service as read locally from path and writable.
func (s *serviceAPI) Bind(ctx context.Context, name, path string) (*domain.Service, error) {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Services.Bind", map[string]any{"name": name, "path": path})

	svc, ok := f.services[name]
	if !ok {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}
	team := svc.Source
	if svc.Binding != nil && svc.Binding.Team != nil {
		team = *svc.Binding.Team
	}
	svc.Source = domain.Source{Kind: domain.SourceLocal, Path: path}
	svc.PackageDir = path
	svc.Binding = &domain.ServiceBinding{
		Mode:     domain.BindingLocal,
		Team:     &team,
		Local:    &domain.LocalCheckout{Path: path},
		Writable: true,
	}
	f.services[name] = svc
	cp := svc
	return &cp, nil
}

// Unbind restores the committed source recorded by Bind.
func (s *serviceAPI) Unbind(ctx context.Context, name string) (*domain.Service, error) {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Services.Unbind", name)

	svc, ok := f.services[name]
	if !ok {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}
	if svc.Binding != nil && svc.Binding.Team != nil {
		svc.Source = *svc.Binding.Team
		svc.PackageDir = svc.Source.Path
	}
	svc.Binding = fakeBinding(svc.Source)
	f.services[name] = svc
	cp := svc
	return &cp, nil
}

// Binding reports the service's binding with no candidates.
func (s *serviceAPI) Binding(ctx context.Context, name string) (*engine.BindingInfo, error) {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Services.Binding", name)

	svc, ok := f.services[name]
	if !ok {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}
	b := svc.Binding
	if b == nil {
		b = fakeBinding(svc.Source)
	}
	return &engine.BindingInfo{Service: name, Binding: *b}, nil
}

// fakeBinding derives a binding from an unoverridden source.
func fakeBinding(src domain.Source) *domain.ServiceBinding {
	if src.Kind == domain.SourceGit {
		s := src
		return &domain.ServiceBinding{Mode: domain.BindingTeam, Team: &s}
	}
	return &domain.ServiceBinding{Mode: domain.BindingLocal, Local: &domain.LocalCheckout{Path: src.Path}, Writable: true}
}
