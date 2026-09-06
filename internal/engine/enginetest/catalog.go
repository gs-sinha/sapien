package enginetest

import (
	"context"
	"sort"
	"strings"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
	"github.com/growsimplee/sapien/internal/errs"
)

func (c *catalogAPI) GetOperation(ctx context.Context, id string) (*domain.Operation, error) {
	f := c.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Catalog.GetOperation", id)

	op, ok := f.operations[id]
	if !ok {
		return nil, errs.New(errs.OperationNotFound, "operation %q not found", id).WithDetail("id", id)
	}
	cp := op
	return &cp, nil
}

// ResolveOperation accepts an ID, "METHOD /path", or a bare operationId.
func (c *catalogAPI) ResolveOperation(ctx context.Context, ref string) (*domain.Operation, error) {
	f := c.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Catalog.ResolveOperation", ref)
	return f.resolveOperationLocked(ref)
}

// resolveOperationLocked implements the ResolveOperation lookup. The caller
// must already hold f.mu.
func (f *Fake) resolveOperationLocked(ref string) (*domain.Operation, error) {
	if op, ok := f.operations[ref]; ok {
		cp := op
		return &cp, nil
	}

	if idx := strings.IndexByte(ref, ' '); idx > 0 {
		method := strings.ToUpper(strings.TrimSpace(ref[:idx]))
		path := strings.TrimSpace(ref[idx+1:])
		for _, op := range f.operations {
			if op.HTTP != nil && strings.EqualFold(op.HTTP.Method, method) && op.HTTP.Path == path {
				cp := op
				return &cp, nil
			}
		}
	}

	for _, op := range f.operations {
		if op.RawOpID != "" && op.RawOpID == ref {
			cp := op
			return &cp, nil
		}
	}

	return nil, errs.New(errs.OperationNotFound, "operation %q not found", ref).WithDetail("ref", ref)
}

func (c *catalogAPI) ListOperations(ctx context.Context, service string) ([]domain.Operation, error) {
	f := c.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Catalog.ListOperations", service)

	out := make([]domain.Operation, 0, len(f.operations))
	for _, op := range f.operations {
		if service != "" && op.ServiceID != service {
			continue
		}
		out = append(out, op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (c *catalogAPI) Fields(ctx context.Context, operationID string) ([]domain.Field, error) {
	f := c.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Catalog.Fields", operationID)

	if _, ok := f.operations[operationID]; !ok {
		return nil, errs.New(errs.OperationNotFound, "operation %q not found", operationID).WithDetail("id", operationID)
	}
	fields := f.fields[operationID]
	out := make([]domain.Field, len(fields))
	copy(out, fields)
	return out, nil
}

func (c *catalogAPI) GetSchema(ctx context.Context, service, name string) (*domain.NamedSchema, error) {
	f := c.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Catalog.GetSchema", map[string]string{"service": service, "name": name})

	key := service + "." + name
	sc, ok := f.schemas[key]
	if !ok {
		return nil, errs.New(errs.ServiceNotFound, "schema %q not found in service %q", name, service).
			WithDetail("service", service).WithDetail("name", name)
	}
	cp := sc
	return &cp, nil
}

func (c *catalogAPI) ListDocs(ctx context.Context, service string) ([]domain.Doc, error) {
	f := c.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Catalog.ListDocs", service)

	out := make([]domain.Doc, 0, len(f.docs))
	for _, d := range f.docs {
		if service != "" && d.ServiceID != service {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (c *catalogAPI) GetDoc(ctx context.Context, service, path string) (*domain.Doc, error) {
	f := c.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Catalog.GetDoc", map[string]string{"service": service, "path": path})

	for _, d := range f.docs {
		if d.ServiceID == service && d.Path == path {
			cp := d
			return &cp, nil
		}
	}
	return nil, errs.New(errs.DocNotFound, "doc %q not found in service %q", path, service).
		WithDetail("service", service).WithDetail("path", path)
}

var _ engine.CatalogAPI = (*catalogAPI)(nil)
