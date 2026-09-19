package enginetest

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/folder"
)

// The example API keeps examples in memory, keyed by id, recording every
// call like the other groups. FromRun builds the example from a run
// already present in f.runs (seed one with Fake.RunResult or Seed).

func (x *exampleAPI) List(ctx context.Context, q domain.ExampleQuery) ([]domain.SavedExample, error) {
	f := x.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Examples.List", q)

	out := make([]domain.SavedExample, 0, len(f.examples))
	for _, ex := range f.examples {
		if q.Operation != "" && ex.Operation != q.Operation {
			continue
		}
		if q.Service != "" && ex.Service != q.Service {
			continue
		}
		if q.Tag != "" && !containsString(ex.Tags, q.Tag) {
			continue
		}
		if q.Text != "" {
			hay := strings.ToLower(ex.ID + " " + ex.Description + " " + strings.Join(ex.Tags, " ") + " " + ex.Folder)
			if !strings.Contains(hay, strings.ToLower(q.Text)) {
				continue
			}
		}
		if q.Folder != "" && !folder.HasPrefix(ex.Folder, folder.Normalize(q.Folder)) {
			continue
		}
		out = append(out, ex)
	}
	sortExamples(out)
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (x *exampleAPI) Get(ctx context.Context, id string) (*domain.SavedExample, error) {
	f := x.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Examples.Get", id)

	ex, ok := f.examples[id]
	if !ok {
		return nil, errs.New(errs.ExampleNotFound, "example %q not found", id).WithDetail("id", id)
	}
	cp := ex
	return &cp, nil
}

func (x *exampleAPI) Create(ctx context.Context, ex domain.SavedExample) (*domain.SavedExample, error) {
	f := x.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Examples.Create", ex)

	if ex.ID == "" || ex.Operation == "" {
		return nil, errs.New(errs.Invalid, "example needs an id and an operation")
	}
	if _, exists := f.examples[ex.ID]; exists {
		return nil, errs.New(errs.Conflict, "example %q already exists", ex.ID).WithDetail("id", ex.ID)
	}
	applyExampleDefaults(&ex)
	f.examples[ex.ID] = ex
	cp := ex
	return &cp, nil
}

func (x *exampleAPI) Update(ctx context.Context, ex domain.SavedExample) (*domain.SavedExample, error) {
	f := x.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Examples.Update", ex)

	existing, ok := f.examples[ex.ID]
	if !ok {
		return nil, errs.New(errs.ExampleNotFound, "example %q not found", ex.ID).WithDetail("id", ex.ID)
	}
	ex.Created = existing.Created
	applyExampleDefaults(&ex)
	ex.Updated = time.Now()
	f.examples[ex.ID] = ex
	cp := ex
	return &cp, nil
}

func (x *exampleAPI) Delete(ctx context.Context, id string) error {
	f := x.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Examples.Delete", id)

	if _, ok := f.examples[id]; !ok {
		return errs.New(errs.ExampleNotFound, "example %q not found", id).WithDetail("id", id)
	}
	delete(f.examples, id)
	return nil
}

func (x *exampleAPI) FromRun(ctx context.Context, req engine.ExampleFromRun) (*domain.SavedExample, error) {
	f := x.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Examples.FromRun", req)

	run, ok := f.runs[req.RunID]
	if !ok {
		return nil, errs.New(errs.RunNotFound, "run %q not found", req.RunID).WithDetail("id", req.RunID)
	}
	if req.ID == "" {
		return nil, errs.New(errs.Invalid, "example id is required")
	}
	if _, exists := f.examples[req.ID]; exists {
		return nil, errs.New(errs.Conflict, "example %q already exists", req.ID).WithDetail("id", req.ID)
	}
	var step *domain.StepResult
	for i := range run.Steps {
		if req.StepID == "" || run.Steps[i].StepID == req.StepID {
			step = &run.Steps[i]
			break
		}
	}
	if step == nil {
		return nil, errs.New(errs.Invalid, "run %q has no step %q", req.RunID, req.StepID)
	}
	ex := domain.SavedExample{
		ID:          req.ID,
		Operation:   step.Operation,
		Description: req.Description,
		Scope:       req.Scope,
		Tags:        req.Tags,
		Verified: &domain.ExampleVerified{
			Env: run.Environment, RunID: run.ID, StepID: step.StepID, At: time.Now(), Source: req.Source,
		},
	}
	if step.Request != nil {
		ex.Body = step.Request.Body
		ex.Headers = map[string]string{}
		for k, v := range step.Request.Headers {
			if strings.EqualFold(k, "Authorization") {
				continue
			}
			ex.Headers[k] = v
		}
	}
	if step.Response != nil {
		ex.Expect = &domain.ExampleExpect{Status: step.Response.Status, Body: step.Response.Body}
	}
	applyExampleDefaults(&ex)
	f.examples[ex.ID] = ex
	cp := ex
	return &cp, nil
}

func (x *exampleAPI) ForOperations(ctx context.Context, operationIDs []string, limit int) ([]domain.SavedExample, error) {
	f := x.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Examples.ForOperations", operationIDs)

	want := map[string]bool{}
	for _, id := range operationIDs {
		want[id] = true
	}
	var out []domain.SavedExample
	for _, ex := range f.examples {
		if want[ex.Operation] {
			out = append(out, ex)
		}
	}
	sortExamples(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (x *exampleAPI) Reindex(ctx context.Context) error {
	f := x.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Examples.Reindex", nil)
	return nil
}

// applyExampleDefaults fills Version, Scope, Service, Created, Updated.
func applyExampleDefaults(ex *domain.SavedExample) {
	if ex.Version == 0 {
		ex.Version = 1
	}
	if ex.Scope == "" {
		ex.Scope = domain.ExampleScopeWorkspace
	}
	if ex.Service == "" {
		if i := strings.Index(ex.Operation, "."); i > 0 {
			ex.Service = ex.Operation[:i]
		}
	}
	now := time.Now()
	if ex.Created.IsZero() {
		ex.Created = now
	}
	if ex.Updated.IsZero() {
		ex.Updated = now
	}
}

// sortExamples orders verified examples first, then newest first, then id.
func sortExamples(out []domain.SavedExample) {
	sort.SliceStable(out, func(i, j int) bool {
		vi, vj := out[i].Verified != nil, out[j].Verified != nil
		if vi != vj {
			return vi
		}
		if !out[i].Updated.Equal(out[j].Updated) {
			return out[i].Updated.After(out[j].Updated)
		}
		return out[i].ID < out[j].ID
	})
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Move re-stamps the stored example's Tier; the fake has no files.
func (x *exampleAPI) Move(ctx context.Context, id, tier string) (*domain.SavedExample, error) {
	f := x.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Examples.Move", map[string]string{"id": id, "tier": tier})
	stored, ok := f.examples[id]
	if !ok {
		return nil, errs.New(errs.ExampleNotFound, "example %q not found", id).WithDetail("id", id)
	}
	if stored.Scope == domain.ExampleScopeService {
		return nil, errs.New(errs.Invalid, "a service-scope example lives in its service; rescope it first")
	}
	stored.Tier = tier
	if tier == domain.TierWorkspace {
		stored.Shipped = domain.ShipUntracked
	} else {
		stored.Shipped = ""
	}
	f.examples[id] = stored
	cp := stored
	return &cp, nil
}

// MoveFolder re-stamps the stored example's Folder; the fake has no files,
// so there is no conflict or read-only check to make, and no cleanup to do.
func (x *exampleAPI) MoveFolder(ctx context.Context, id, newFolder string) (*domain.SavedExample, error) {
	f := x.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Examples.MoveFolder", map[string]string{"id": id, "folder": newFolder})
	stored, ok := f.examples[id]
	if !ok {
		return nil, errs.New(errs.ExampleNotFound, "example %q not found", id).WithDetail("id", id)
	}
	norm, err := folder.NormalizeAndValidate(newFolder)
	if err != nil {
		return nil, err
	}
	stored.Folder = norm
	f.examples[id] = stored
	cp := stored
	return &cp, nil
}

// Commit marks the stored example as committed but not pushed.
func (x *exampleAPI) Commit(ctx context.Context, id, message string) (*domain.SavedExample, error) {
	f := x.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Examples.Commit", map[string]string{"id": id, "message": message})
	stored, ok := f.examples[id]
	if !ok {
		return nil, errs.New(errs.ExampleNotFound, "example %q not found", id).WithDetail("id", id)
	}
	if stored.Tier != "" && stored.Tier != domain.TierWorkspace {
		return nil, errs.New(errs.Invalid, "only a workspace-tier example can be committed; %q is %s", id, stored.Tier)
	}
	stored.Shipped = domain.ShipUnpushed
	f.examples[id] = stored
	cp := stored
	return &cp, nil
}
