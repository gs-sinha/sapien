package enginetest

import (
	"context"
	"sort"
	"strings"
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

// BindWith is Bind for the fake; Force has nothing to override since the
// fake validates no filesystem. An empty name infers the service from a
// candidate the test seeded with the same path, else conflicts.
func (s *serviceAPI) BindWith(ctx context.Context, name, path string, opts engine.BindOptions) (*domain.Service, error) {
	if name == "" {
		f := s.f()
		f.mu.Lock()
		for _, svc := range f.services {
			if svc.Binding != nil && svc.Binding.Local != nil && svc.Binding.Local.Path == path {
				name = svc.Name
				break
			}
		}
		f.mu.Unlock()
		if name == "" {
			return nil, errs.New(errs.Invalid, "no registered service is cloned from the repository at %s", path).WithDetail("path", path)
		}
	}
	return s.Bind(ctx, name, path)
}

// BrowseCheckouts returns an empty listing rooted at dir.
func (s *serviceAPI) BrowseCheckouts(ctx context.Context, name, dir string) (*engine.DirListing, error) {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Services.BrowseCheckouts", map[string]string{"name": name, "dir": dir})
	if _, ok := f.services[name]; !ok {
		return nil, errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}
	if dir == "" {
		dir = "/home"
	}
	return &engine.DirListing{Path: dir, Entries: []engine.DirEntry{}}, nil
}

// AddFromCheckout registers a git source named after the path's last
// element and binds the path, mirroring what the real engine does with the
// checkout's origin.
func (s *serviceAPI) AddFromCheckout(ctx context.Context, name, path string, opts engine.AddFromCheckoutOptions) (*domain.Service, error) {
	if name == "" {
		name = path[strings.LastIndex(path, "/")+1:]
	}
	src := domain.Source{Kind: domain.SourceGit, URL: "git@github.com:org/" + name + ".git", Ref: opts.Ref}
	if _, err := s.Add(ctx, name, src); err != nil {
		return nil, err
	}
	f := s.f()
	f.mu.Lock()
	f.recordLocked("Services.AddFromCheckout", map[string]any{"name": name, "path": path, "ref": opts.Ref, "force": opts.Force})
	f.mu.Unlock()
	return s.Bind(ctx, name, path)
}
