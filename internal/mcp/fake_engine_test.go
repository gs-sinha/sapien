package mcp

// A minimal in-memory fake of engine.Engine for exercising the MCP server in
// tests. internal/engine/enginetest does not exist yet (another agent owns
// it), so per the task instructions this package provides its own fake
// rather than depending on it.

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// fakeState is the shared, mutex-protected in-memory store behind every
// fakeEngine sub-API.
type fakeState struct {
	mu       sync.Mutex
	ws       domain.Workspace
	services []domain.Service
	ops      []domain.Operation
	fields   map[string][]domain.Field
	schemas  []domain.NamedSchema
	docs     []domain.Doc
	flows    []domain.Flow
	memories []domain.Memory
	runs     []domain.Run
	examples []domain.SavedExample
	envs     []domain.Environment
	refs     map[string]string
	nextMem  int
	nextRun  int
	// lastRunOptions records the engine.RunOptions the most recent
	// RunFlow/RunFlowSource call received, so a test can assert run_flow
	// passed resume_from/from_step/until_step through unchanged.
	lastRunOptions engine.RunOptions
}

// fakeEngine implements engine.Engine.
type fakeEngine struct{ st *fakeState }

func newFakeEngine(ws domain.Workspace) *fakeEngine {
	return &fakeEngine{st: &fakeState{ws: ws, fields: map[string][]domain.Field{}, refs: map[string]string{}}}
}

func (e *fakeEngine) Workspace() *domain.Workspace { return &e.st.ws }
func (e *fakeEngine) Services() engine.ServiceAPI  { return fakeServices{e.st} }
func (e *fakeEngine) Catalog() engine.CatalogAPI   { return fakeCatalog{e.st} }
func (e *fakeEngine) Search() engine.SearchAPI     { return fakeSearch{e.st} }
func (e *fakeEngine) Flows() engine.FlowAPI        { return fakeFlows{e.st} }
func (e *fakeEngine) Runner() engine.RunnerAPI     { return fakeRunner{e.st} }
func (e *fakeEngine) Runs() engine.RunAPI          { return fakeRuns{e.st} }
func (e *fakeEngine) Memories() engine.MemoryAPI   { return fakeMemories{e.st} }
func (e *fakeEngine) Examples() engine.ExampleAPI  { return fakeExamples{e.st} }
func (e *fakeEngine) Context() engine.ContextAPI   { return fakeContext{e.st} }
func (e *fakeEngine) Envs() engine.EnvAPI          { return fakeEnvs{e.st} }
func (e *fakeEngine) Events() engine.EventAPI      { return fakeEvents{e.st} }
func (e *fakeEngine) Close() error                 { return nil }

var _ engine.Engine = (*fakeEngine)(nil)

// --- ServiceAPI -----------------------------------------------------

type fakeServices struct{ st *fakeState }

func (a fakeServices) List(context.Context) ([]domain.Service, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	return append([]domain.Service{}, a.st.services...), nil
}

func (a fakeServices) Get(_ context.Context, name string) (*domain.Service, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.services {
		if a.st.services[i].Name == name {
			c := a.st.services[i]
			return &c, nil
		}
	}
	return nil, errs.New(errs.ServiceNotFound, "service %q not found", name)
}

// Add registers a service the way the real engine would report it: the
// source is stored verbatim (the fake has no filesystem to sync), a missing
// name is derived from the path or URL, and a duplicate name conflicts. A
// path ending in "warn" yields one warning, so tools can render it.
func (a fakeServices) Add(_ context.Context, name string, src domain.Source) (*domain.Service, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	if name == "" {
		base := src.Path
		if src.Kind == domain.SourceGit {
			base = strings.TrimSuffix(src.URL, ".git")
		}
		name = base[strings.LastIndexAny(base, "/:")+1:]
	}
	for _, s := range a.st.services {
		if s.Name == name {
			return nil, errs.New(errs.Conflict, "service %q already exists", name).WithDetail("name", name)
		}
	}
	svc := domain.Service{ID: name, Name: name, Source: src, Status: domain.SyncOK, OperationCount: 3}
	if strings.HasSuffix(src.Path, "warn") {
		svc.Warnings = []domain.LintWarning{{Code: "W_NO_OPERATION_ID", Message: "GET /v1/stats has no operationId",
			Source: &domain.SourceLoc{File: src.Path + "/api/openapi.yaml", Line: 42}}}
	}
	a.st.services = append(a.st.services, svc)
	cp := svc
	return &cp, nil
}
func (a fakeServices) Remove(context.Context, string) error {
	return errs.New(errs.NotImplemented, "Remove not implemented in fake")
}

// Sync re-syncs one named service (or, when name is "", every registered
// service), returning it/them as-is -- the fake has no filesystem to
// actually re-read, but this is enough to exercise sync_service and
// add_service's E_CONFLICT -> resync path.
func (a fakeServices) Sync(_ context.Context, name string) ([]domain.Service, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	if name == "" {
		return append([]domain.Service{}, a.st.services...), nil
	}
	for _, s := range a.st.services {
		if s.Name == name {
			return []domain.Service{s}, nil
		}
	}
	return nil, errs.New(errs.ServiceNotFound, "service %q not found", name)
}
func (a fakeServices) Reindex(context.Context) error { return nil }

// --- CatalogAPI -----------------------------------------------------

type fakeCatalog struct{ st *fakeState }

func (a fakeCatalog) GetOperation(_ context.Context, id string) (*domain.Operation, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.ops {
		if a.st.ops[i].ID == id {
			c := a.st.ops[i]
			return &c, nil
		}
	}
	return nil, errs.New(errs.OperationNotFound, "operation %q not found", id)
}

func (a fakeCatalog) ResolveOperation(_ context.Context, ref string) (*domain.Operation, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.ops {
		op := a.st.ops[i]
		if op.ID == ref || op.RawOpID == ref {
			c := op
			return &c, nil
		}
		if op.HTTP != nil && op.HTTP.Method+" "+op.HTTP.Path == ref {
			c := op
			return &c, nil
		}
	}
	return nil, errs.New(errs.OperationNotFound, "operation %q not found", ref)
}

func (a fakeCatalog) ListOperations(_ context.Context, service string) ([]domain.Operation, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	var out []domain.Operation
	for _, op := range a.st.ops {
		if service == "" || op.ServiceID == service {
			out = append(out, op)
		}
	}
	return out, nil
}

func (a fakeCatalog) Fields(_ context.Context, operationID string) ([]domain.Field, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	return append([]domain.Field{}, a.st.fields[operationID]...), nil
}

func (a fakeCatalog) GetSchema(_ context.Context, service, name string) (*domain.NamedSchema, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.schemas {
		if a.st.schemas[i].ServiceID == service && a.st.schemas[i].Name == name {
			c := a.st.schemas[i]
			return &c, nil
		}
	}
	return nil, errs.New(errs.Invalid, "schema %s.%s not found", service, name)
}

func (a fakeCatalog) ListDocs(_ context.Context, service string) ([]domain.Doc, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	var out []domain.Doc
	for _, d := range a.st.docs {
		if service == "" || d.ServiceID == service {
			out = append(out, d)
		}
	}
	return out, nil
}

func (a fakeCatalog) GetDoc(_ context.Context, service, path string) (*domain.Doc, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.docs {
		if a.st.docs[i].ServiceID == service && a.st.docs[i].Path == path {
			c := a.st.docs[i]
			return &c, nil
		}
	}
	return nil, errs.New(errs.DocNotFound, "doc %s/%s not found", service, path)
}

// --- SearchAPI -----------------------------------------------------

type fakeSearch struct{ st *fakeState }

func (a fakeSearch) Operations(_ context.Context, query string, opts domain.SearchOptions) ([]domain.SearchResult, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	q := strings.ToLower(query)
	var out []domain.SearchResult
	for _, op := range a.st.ops {
		if opts.Service != "" && op.ServiceID != opts.Service {
			continue
		}
		if opts.Method != "" && methodOf(op) != opts.Method {
			continue
		}
		var score float64
		var matched []string
		if q == "" || strings.Contains(strings.ToLower(op.ID), q) {
			score += 1
			matched = append(matched, "op_id")
		}
		if strings.Contains(strings.ToLower(op.Summary), q) {
			score += 0.5
			matched = append(matched, "summary")
		}
		if strings.Contains(strings.ToLower(pathOf(op)), q) {
			score += 0.5
			matched = append(matched, "path")
		}
		if score == 0 {
			continue
		}
		out = append(out, domain.SearchResult{Operation: op, Score: score, MatchedOn: matched})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

func (a fakeSearch) Docs(_ context.Context, query string, opts domain.SearchOptions) ([]domain.DocSearchResult, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	q := strings.ToLower(query)
	var out []domain.DocSearchResult
	for _, d := range a.st.docs {
		if opts.Service != "" && d.ServiceID != opts.Service {
			continue
		}
		for _, sec := range d.Sections {
			if q != "" && !strings.Contains(strings.ToLower(sec.Body), q) && !strings.Contains(strings.ToLower(sec.Heading), q) {
				continue
			}
			snippet := sec.Body
			if len(snippet) > 80 {
				snippet = snippet[:80]
			}
			out = append(out, domain.DocSearchResult{
				Service: d.ServiceID, DocID: d.ID, Path: d.Path, Title: d.Title,
				SectionID: sec.ID, Heading: sec.Heading, Snippet: snippet, Refs: sec.Refs, Score: 1,
			})
		}
	}
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

// --- FlowAPI -----------------------------------------------------

type fakeFlows struct{ st *fakeState }

func (a fakeFlows) List(_ context.Context, query string) ([]domain.FlowSummary, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	var out []domain.FlowSummary
	for _, f := range a.st.flows {
		if query != "" && !strings.Contains(strings.ToLower(f.Name+" "+f.ID), strings.ToLower(query)) {
			continue
		}
		var ops []string
		for _, st := range f.Steps {
			ops = append(ops, st.Call)
		}
		out = append(out, domain.FlowSummary{
			ID: f.ID, Name: f.Name, Path: f.Path, OwnerKind: f.OwnerKind, OwnerID: f.OwnerID,
			Tags: f.Tags, Operations: ops, StepCount: len(f.Steps), Hash: "h", Updated: time.Now(),
		})
	}
	return out, nil
}

func (a fakeFlows) Get(_ context.Context, id string) (*domain.Flow, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.flows {
		if a.st.flows[i].ID == id {
			c := a.st.flows[i]
			return &c, nil
		}
	}
	return nil, errs.New(errs.FlowNotFound, "flow %q not found", id)
}

func (a fakeFlows) Parse(_ context.Context, yamlSrc string) (*domain.Flow, error) {
	return &domain.Flow{Source: yamlSrc}, nil
}

func (a fakeFlows) Validate(_ context.Context, yamlSrc string) (*domain.ValidationResult, error) {
	if strings.Contains(yamlSrc, "INVALID") {
		return &domain.ValidationResult{Valid: false, Diagnostics: []domain.Diagnostic{{
			Code: "E_OPERATION_NOT_FOUND", Severity: domain.SeverityError,
			Message: "unknown operation \"bogus.op\"", Line: 3,
			Suggestions: []string{"rider-service.getRider"},
		}}}, nil
	}
	// A "WARNSTEP" marker (test-fixture only) yields a valid result that
	// still carries a warning-severity diagnostic, so create_flow/
	// update_flow/patch_flow's FlowSaveResult.Diagnostics (warnings only,
	// since an error would have rejected the write) has something to carry.
	if strings.Contains(yamlSrc, "WARNSTEP") {
		return &domain.ValidationResult{Valid: true, Diagnostics: []domain.Diagnostic{{
			Code: "W_UNUSED_EXTRACT", Severity: domain.SeverityWarning,
			Message: "extract \"x\" is never referenced", Line: 5,
		}}}, nil
	}
	return &domain.ValidationResult{Valid: true}, nil
}

// fakeFlowDoc is a minimal YAML shape (version/id/name/tags/setup/steps/
// teardown) the fake decodes create_flow/update_flow's source into, so
// tests can assert on real step/setup/teardown counts and a caller-given
// `id:` instead of the fake always inventing "step1"/"flow-N".
type fakeFlowDoc struct {
	Version     int            `yaml:"version"`
	ID          string         `yaml:"id"`
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Tags        []string       `yaml:"tags"`
	Inputs      map[string]any `yaml:"inputs"`
	Setup       []domain.Step  `yaml:"setup"`
	Steps       []domain.Step  `yaml:"steps"`
	Teardown    []domain.Step  `yaml:"teardown"`
}

// parseFakeFlow decodes yamlSrc via fakeFlowDoc; a decode error yields a
// bare, mostly-empty domain.Flow (Create/Update's own Validate call, run
// first, is what actually rejects malformed source in these tests).
func parseFakeFlow(yamlSrc string) domain.Flow {
	var fd fakeFlowDoc
	_ = yaml.Unmarshal([]byte(yamlSrc), &fd)
	return domain.Flow{
		Version: fd.Version, ID: fd.ID, Name: fd.Name, Description: fd.Description, Tags: fd.Tags,
		Setup: fd.Setup, Steps: fd.Steps, Teardown: fd.Teardown, Source: yamlSrc,
	}
}

func (a fakeFlows) Create(ctx context.Context, yamlSrc string, path string) (*domain.Flow, error) {
	res, _ := a.Validate(ctx, yamlSrc)
	if !res.Valid {
		return nil, errs.New(errs.FlowInvalid, "flow failed validation").WithDetail("diagnostics", res.Diagnostics)
	}
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	f := parseFakeFlow(yamlSrc)
	if f.ID == "" {
		f.ID = fmt.Sprintf("flow-%d", len(a.st.flows)+1)
	}
	if path == "" {
		path = f.ID + ".flow.yaml"
	}
	f.Path = path
	f.OwnerKind = "workspace"
	if f.Name == "" {
		f.Name = f.ID
	}
	a.st.flows = append(a.st.flows, f)
	c := f
	return &c, nil
}

func (a fakeFlows) Update(ctx context.Context, id string, yamlSrc string) (*domain.Flow, error) {
	res, _ := a.Validate(ctx, yamlSrc)
	if !res.Valid {
		return nil, errs.New(errs.FlowInvalid, "flow failed validation").WithDetail("diagnostics", res.Diagnostics)
	}
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.flows {
		if a.st.flows[i].ID == id {
			path, owner := a.st.flows[i].Path, a.st.flows[i].OwnerKind
			f := parseFakeFlow(yamlSrc)
			f.ID, f.Path, f.OwnerKind = id, path, owner
			a.st.flows[i] = f
			c := f
			return &c, nil
		}
	}
	return nil, errs.New(errs.FlowNotFound, "flow %q not found", id)
}

func (a fakeFlows) Delete(context.Context, string) error { return nil }

func (a fakeFlows) Reference(_ context.Context, topic string) (string, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	if t, ok := a.st.refs[topic]; ok {
		return t, nil
	}
	return "", errs.New(errs.Invalid, "unknown reference topic %q", topic)
}

// --- RunnerAPI -----------------------------------------------------

type fakeRunner struct{ st *fakeState }

func fakeStepResult(idx int, stepID, op string) domain.StepResult {
	var body any = map[string]any{"riderId": "r1", "qcomSkill": true}
	// bigResponse exists purely so execute_api/run_flow tests can exercise
	// the 16 KB response-body cap (PLAN §23.3).
	if op == "rider-service.bigResponse" {
		body = map[string]any{"data": strings.Repeat("x", 20*1024)}
	}
	return domain.StepResult{
		StepID: stepID, Index: idx, Operation: op, Status: domain.StepPassed,
		Request:    &domain.RequestRecord{Method: "GET", URL: "https://staging.example.com/v1/riders/r1"},
		Response:   &domain.ResponseRecord{Status: 200, Body: body},
		Assertions: []domain.AssertionResult{{Expr: "response.status == 200", Passed: true}},
		Started:    time.Now(), Finished: time.Now(),
	}
}

func (a fakeRunner) record(flowID, env string, steps []domain.StepResult) *domain.Run {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	a.st.nextRun++
	id := fmt.Sprintf("run_%d", a.st.nextRun)
	var passed, failed int
	for _, st := range steps {
		switch st.Status {
		case domain.StepPassed:
			passed++
		case domain.StepFailed:
			failed++
		}
	}
	run := domain.Run{
		ID: id, FlowID: flowID, Environment: env, Status: domain.RunPassed,
		Started: time.Now(), Finished: time.Now(), Steps: steps, Trigger: "mcp",
		Summary: domain.RunSummary{StepsTotal: len(steps), StepsPassed: passed, StepsFailed: failed},
	}
	if failed > 0 {
		run.Status = domain.RunFailed
	}
	a.st.runs = append(a.st.runs, run)
	c := run
	return &c
}

func (a fakeRunner) RunFlow(_ context.Context, flowID string, opts engine.RunOptions) (*domain.Run, error) {
	a.st.mu.Lock()
	a.st.lastRunOptions = opts
	var flow *domain.Flow
	for i := range a.st.flows {
		if a.st.flows[i].ID == flowID {
			c := a.st.flows[i]
			flow = &c
		}
	}
	a.st.mu.Unlock()
	if flow == nil {
		return nil, errs.New(errs.FlowNotFound, "flow %q not found", flowID)
	}
	var steps []domain.StepResult
	for i, st := range flow.Steps {
		steps = append(steps, fakeStepResult(i, st.ID, st.Call))
	}
	return a.record(flowID, opts.Environment, steps), nil
}

func (a fakeRunner) RunFlowSource(_ context.Context, _ string, opts engine.RunOptions) (*domain.Run, error) {
	a.st.mu.Lock()
	a.st.lastRunOptions = opts
	a.st.mu.Unlock()
	return a.record("", opts.Environment, []domain.StepResult{fakeStepResult(0, "step1", "rider-service.getRider")}), nil
}

func (a fakeRunner) Call(_ context.Context, req engine.CallRequest) (*domain.Run, error) {
	return a.record("", req.Env, []domain.StepResult{fakeStepResult(0, "call", req.Operation)}), nil
}

func (a fakeRunner) Cancel(context.Context, string) error { return nil }

// --- RunAPI -----------------------------------------------------

type fakeRuns struct{ st *fakeState }

func (a fakeRuns) List(context.Context, domain.RunFilter) ([]domain.Run, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	var out []domain.Run
	for _, r := range a.st.runs {
		c := r
		c.Steps = nil
		out = append(out, c)
	}
	return out, nil
}

func (a fakeRuns) Get(_ context.Context, id string) (*domain.Run, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.runs {
		if a.st.runs[i].ID == id {
			c := a.st.runs[i]
			return &c, nil
		}
	}
	return nil, errs.New(errs.RunNotFound, "run %q not found", id)
}

func (a fakeRuns) Pin(context.Context, string, bool) error { return nil }
func (a fakeRuns) Purge(context.Context, int) (int, error) { return 0, nil }

// --- MemoryAPI -----------------------------------------------------

type fakeMemories struct{ st *fakeState }

func (a fakeMemories) Create(_ context.Context, m domain.Memory) (*domain.Memory, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	a.st.nextMem++
	m.ID = fmt.Sprintf("mem_%d", a.st.nextMem)
	if m.Status == "" {
		m.Status = domain.MemoryActive
	}
	now := time.Now()
	m.Created, m.Updated = now, now
	a.st.memories = append(a.st.memories, m)
	c := m
	return &c, nil
}

func (a fakeMemories) Get(_ context.Context, id string) (*domain.Memory, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.memories {
		if a.st.memories[i].ID == id {
			c := a.st.memories[i]
			return &c, nil
		}
	}
	return nil, errs.New(errs.MemoryNotFound, "memory %q not found", id)
}

func (a fakeMemories) Update(_ context.Context, m domain.Memory) (*domain.Memory, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.memories {
		if a.st.memories[i].ID == m.ID {
			a.st.memories[i] = m
			c := m
			return &c, nil
		}
	}
	return nil, errs.New(errs.MemoryNotFound, "memory %q not found", m.ID)
}

func (a fakeMemories) Delete(context.Context, string) error { return nil }

func (a fakeMemories) List(context.Context, domain.MemoryQuery) ([]domain.Memory, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	return append([]domain.Memory{}, a.st.memories...), nil
}

func (a fakeMemories) Search(_ context.Context, q domain.MemoryQuery) ([]domain.ScoredMemory, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	ql := strings.ToLower(q.Text)
	var out []domain.ScoredMemory
	for _, m := range a.st.memories {
		if q.Operation != "" && m.Subject.Operation != q.Operation {
			continue
		}
		if q.Service != "" && m.Subject.Service != q.Service {
			continue
		}
		if ql != "" && !strings.Contains(strings.ToLower(m.Text), ql) {
			continue
		}
		out = append(out, domain.ScoredMemory{Memory: m, Score: 1, Reasons: []string{"lexical"}})
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (a fakeMemories) Relevant(_ context.Context, subjects []domain.Subject, limit int) ([]domain.ScoredMemory, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	var out []domain.ScoredMemory
	for _, m := range a.st.memories {
		for _, subj := range subjects {
			if subj.Operation != "" && m.Subject.Operation == subj.Operation {
				out = append(out, domain.ScoredMemory{Memory: m, Score: 1, Reasons: []string{"operation match"}})
				break
			}
			if subj.Service != "" && m.Subject.Service == subj.Service {
				out = append(out, domain.ScoredMemory{Memory: m, Score: 0.8, Reasons: []string{"service match"}})
				break
			}
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (a fakeMemories) PromotionTarget(_ context.Context, id string) (*engine.PromotionTarget, error) {
	a.st.mu.Lock()
	var mem *domain.Memory
	for i := range a.st.memories {
		if a.st.memories[i].ID == id {
			c := a.st.memories[i]
			mem = &c
		}
	}
	a.st.mu.Unlock()
	if mem == nil {
		return nil, errs.New(errs.MemoryNotFound, "memory %q not found", id)
	}
	return &engine.PromotionTarget{
		Kind: "openapi", File: "rider-service/openapi.yaml", Line: 42,
		Pointer: "/paths/~1v1~1riders~1{riderId}/get", Current: "Get a rider by id.", Memory: *mem,
	}, nil
}

func (a fakeMemories) Reindex(context.Context) error { return nil }

// --- ContextAPI -----------------------------------------------------

type fakeContext struct{ st *fakeState }

func (a fakeContext) Build(_ context.Context, req domain.ContextRequest) (*domain.ContextBundle, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()

	var opIDs []string
	if len(req.Operations) > 0 {
		opIDs = req.Operations
	} else {
		for _, op := range a.st.ops {
			opIDs = append(opIDs, op.ID)
		}
	}

	bundle := &domain.ContextBundle{Intent: req.Intent, EstimatedTokens: 100}
	var subjects []domain.Subject
	for _, id := range opIDs {
		for _, op := range a.st.ops {
			if op.ID == id {
				bundle.Operations = append(bundle.Operations, domain.OperationContext{
					Tier: domain.TierContract, ID: op.ID, Method: methodOf(op), Path: pathOf(op), Summary: op.Summary,
				})
				subjects = append(subjects, domain.Subject{Operation: op.ID, Service: op.ServiceID})
			}
		}
	}
	for _, m := range a.st.memories {
		for _, subj := range subjects {
			if subj.Operation != "" && m.Subject.Operation == subj.Operation {
				bundle.Memories = append(bundle.Memories, domain.MemoryContext{
					Tier: domain.TierMemory, ID: m.ID, Type: m.Type, Scope: m.Scope,
					Source: m.Source.Kind, Subject: m.Subject, Text: m.Text,
				})
				break
			}
		}
	}
	for _, d := range a.st.docs {
		for _, sec := range d.Sections {
			for _, ref := range sec.Refs {
				for _, id := range opIDs {
					if ref.Kind == domain.RefOperation && ref.Value == id {
						bundle.Docs = append(bundle.Docs, domain.DocContext{
							Tier: domain.TierDocumentation, Service: d.ServiceID, Path: d.Path, Heading: sec.Heading, Body: sec.Body,
						})
					}
				}
			}
		}
	}
	for _, f := range a.st.flows {
		for _, st := range f.Steps {
			for _, id := range opIDs {
				if st.Call != id {
					continue
				}
				var steps []string
				for _, s2 := range f.Steps {
					steps = append(steps, s2.ID+": "+s2.Call)
				}
				bundle.Flows = append(bundle.Flows, domain.FlowContext{ID: f.ID, Name: f.Name, Steps: steps})
			}
		}
	}
	return bundle, nil
}

// --- EnvAPI -----------------------------------------------------

type fakeEnvs struct{ st *fakeState }

func (a fakeEnvs) List(context.Context) ([]domain.Environment, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	return append([]domain.Environment{}, a.st.envs...), nil
}

func (a fakeEnvs) Get(_ context.Context, name string) (*domain.Environment, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.envs {
		if a.st.envs[i].Name == name {
			c := a.st.envs[i]
			return &c, nil
		}
	}
	return nil, errs.New(errs.EnvNotFound, "environment %q not found", name)
}

func (a fakeEnvs) Default(context.Context) (string, error)         { return "staging", nil }
func (a fakeEnvs) SetDefault(context.Context, string) error        { return nil }
func (a fakeEnvs) SetSecret(context.Context, string, string) error { return nil }
func (a fakeEnvs) ListSecrets(context.Context) ([]string, error)   { return nil, nil }
func (a fakeEnvs) DeleteSecret(context.Context, string) error      { return nil }

// --- EventAPI -----------------------------------------------------

type fakeEvents struct{ st *fakeState }

func (a fakeEvents) Subscribe(context.Context) (<-chan domain.Event, func()) {
	ch := make(chan domain.Event)
	return ch, func() { close(ch) }
}

// --- fixture -----------------------------------------------------

// newFixtureEngine builds a fakeEngine seeded with one service
// (rider-service), two operations (a GET and a POST), a schema, a doc, a
// flow, a memory, and three environments (staging, qa: non-production;
// production: production). Every tool test in this package is written
// against these fixed IDs.
func newFixtureEngine() *fakeEngine {
	ws := domain.Workspace{Name: "test-workspace", Dir: "/workspace", DefaultEnvironment: "staging"}
	e := newFakeEngine(ws)
	st := e.st

	st.services = []domain.Service{{
		ID: "rider-service", Name: "rider-service", Description: "Rider allocation service",
		Owners: []string{"team-rider"}, Concepts: []string{"allocation"},
		Environments:   map[string]domain.EnvHint{"staging": {BaseURL: "https://staging.example.com"}},
		Status:         domain.SyncOK,
		OperationCount: 2,
	}}

	getRider := domain.Operation{
		ID: "rider-service.getRider", ServiceID: "rider-service", Protocol: domain.ProtocolHTTP,
		HTTP:    &domain.HTTPBinding{Method: "GET", Path: "/v1/riders/{riderId}"},
		RawOpID: "getRider", Summary: "Get a rider by id",
		Params: []domain.Param{{
			Name: "riderId", In: domain.InPath, Required: true,
			Schema: &domain.Schema{Kind: domain.KindString}, Description: "the rider id",
		}},
		Responses: []domain.Response{{
			Status: "200",
			Schema: &domain.Schema{
				Kind: domain.KindObject, PropertyOrder: []string{"riderId", "qcomSkill"},
				Properties: map[string]*domain.Schema{
					"riderId":   {Kind: domain.KindString},
					"qcomSkill": {Kind: domain.KindBoolean, Description: "qcom allocation skill flag"},
				},
			},
		}},
		Security: []domain.SecurityRequirement{{Scheme: "bearerAuth", Type: "http"}},
		Hash:     "h1",
	}
	createRider := domain.Operation{
		ID: "rider-service.createRider", ServiceID: "rider-service", Protocol: domain.ProtocolHTTP,
		HTTP:    &domain.HTTPBinding{Method: "POST", Path: "/v1/riders"},
		RawOpID: "createRider", Summary: "Create a rider",
		RequestBody: &domain.Body{
			ContentType: "application/json", Required: true,
			Schema: &domain.Schema{
				Kind: domain.KindObject, PropertyOrder: []string{"name"}, Required: []string{"name"},
				Properties: map[string]*domain.Schema{"name": {Kind: domain.KindString, Description: "rider name"}},
			},
		},
		Responses: []domain.Response{{
			Status: "201",
			Schema: &domain.Schema{
				Kind: domain.KindObject, PropertyOrder: []string{"riderId"},
				Properties: map[string]*domain.Schema{"riderId": {Kind: domain.KindString}},
			},
		}},
		Hash: "h2",
	}
	bigResponse := domain.Operation{
		ID: "rider-service.bigResponse", ServiceID: "rider-service", Protocol: domain.ProtocolHTTP,
		HTTP:    &domain.HTTPBinding{Method: "GET", Path: "/v1/riders/big"},
		RawOpID: "bigResponse", Summary: "Get a response over the MCP body cap (test fixture only)",
		Responses: []domain.Response{{Status: "200"}},
		Hash:      "h3",
	}
	st.ops = []domain.Operation{getRider, createRider, bigResponse}
	st.fields[getRider.ID] = []domain.Field{
		{OperationID: getRider.ID, Path: "request.path.riderId", Type: "string", Required: true, Description: "the rider id"},
		{OperationID: getRider.ID, Path: "response.200.body.riderId", Type: "string"},
		{OperationID: getRider.ID, Path: "response.200.body.qcomSkill", Type: "boolean", Description: "qcom allocation skill flag"},
	}

	st.schemas = []domain.NamedSchema{{
		ServiceID: "rider-service", Name: "Rider", Hash: "hs",
		Schema: &domain.Schema{
			Kind: domain.KindObject, PropertyOrder: []string{"riderId", "qcomSkill"}, Required: []string{"riderId"},
			Properties: map[string]*domain.Schema{
				"riderId":   {Kind: domain.KindString},
				"qcomSkill": {Kind: domain.KindBoolean, Description: "qcom allocation skill flag"},
			},
		},
		UsedBy: []string{"rider-service.getRider"},
	}}

	st.docs = []domain.Doc{{
		ID: "rider-service/docs/allocation.md", ServiceID: "rider-service", Path: "docs/allocation.md",
		Title: "Allocation", Source: domain.DocSourceFile,
		Sections: []domain.DocSection{{
			ID: "rider-service/docs/allocation.md#allocation-rules", Ord: 0, Heading: "Allocation rules", Level: 2,
			Body: "Riders with qcomSkill are allocated to QCOM orders first.",
			Refs: []domain.DocRef{{Kind: domain.RefOperation, Value: "rider-service.getRider"}},
		}},
	}}

	st.flows = []domain.Flow{{
		Version: 1, ID: "rider-flow", Name: "Get rider", Path: "flows/rider-flow.flow.yaml", OwnerKind: "workspace",
		Steps:  []domain.Step{{ID: "get", Call: "rider-service.getRider", Input: map[string]any{"riderId": "r1"}}},
		Source: "version: 1\nid: rider-flow\nname: Get rider\nsteps:\n  - id: get\n    call: rider-service.getRider\n    input:\n      riderId: r1\n",
	}}

	st.memories = []domain.Memory{{
		ID: "mem_seed1", Type: domain.MemoryInvariant, Scope: domain.ScopeWorkspace,
		Subject: domain.Subject{Service: "rider-service", Operation: "rider-service.getRider", Field: "qcomSkill"},
		Source:  domain.MemorySource{Kind: "user"}, Status: domain.MemoryActive,
		Created: time.Now(), Updated: time.Now(),
		Text: "qcomSkill must be true for QCOM allocation tests.",
	}}

	st.envs = []domain.Environment{
		{Version: 1, Name: "staging", Production: false},
		{Version: 1, Name: "qa", Production: false},
		{Version: 1, Name: "production", Production: true},
	}

	st.refs = map[string]string{
		"sapien":      "# What Sapien is\n\nA shared index of the services around this repo; get_api returns a request_example.\n",
		"flow":        "# Flow DSL\n\nA flow is a sequence of steps, each calling one operation.\n",
		"memory":      "# Memory DSL\n\nA memory has a subject, a type, and Markdown text.\n",
		"expressions": "# Expressions\n\nCEL expressions evaluate against the step response.\n",
		"service":     "# Service package reference\n\napi/openapi.yaml, api/service.yaml, api/docs/*.md; then add_service.\n",
	}

	// Two seeded examples (PLAN §34b) so list_examples/get_example/get_api
	// have something fixed to find: a verified one (as if saved from a run)
	// on getRider, and an unverified hand-written draft on createRider.
	getRiderExample := domain.SavedExample{
		ID: "rider-get-example", Operation: getRider.ID,
		Description: "Get rider r1: known-good, verified against staging.",
		Scope:       domain.ExampleScopeWorkspace,
		Input:       map[string]any{"riderId": "r1"},
		Expect:      &domain.ExampleExpect{Status: 200, Body: map[string]any{"riderId": "r1", "qcomSkill": true}},
		Verified: &domain.ExampleVerified{
			Env: "staging", RunID: "run_seed1", StepID: "call", At: time.Now(),
			Source: &domain.MemorySource{Kind: "user"},
		},
		Tags: []string{"happy-path"},
	}
	fakeExampleDefaults(&getRiderExample)

	createRiderDraft := domain.SavedExample{
		ID: "rider-create-draft", Operation: createRider.ID,
		Description: "Hand-written draft body for createRider; not yet run.",
		Scope:       domain.ExampleScopeWorkspace,
		Body:        map[string]any{"name": "Asha"},
		Tags:        []string{"draft"},
	}
	fakeExampleDefaults(&createRiderDraft)

	st.examples = []domain.SavedExample{getRiderExample, createRiderDraft}

	return e
}

// --- ExampleAPI -----------------------------------------------------

type fakeExamples struct{ st *fakeState }

func (a fakeExamples) find(id string) int {
	for i := range a.st.examples {
		if a.st.examples[i].ID == id {
			return i
		}
	}
	return -1
}

func (a fakeExamples) List(_ context.Context, q domain.ExampleQuery) ([]domain.SavedExample, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	var out []domain.SavedExample
	for _, ex := range a.st.examples {
		if q.Operation != "" && ex.Operation != q.Operation {
			continue
		}
		if q.Service != "" && ex.Service != q.Service {
			continue
		}
		if q.Tag != "" {
			found := false
			for _, t := range ex.Tags {
				if t == q.Tag {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		if q.Text != "" && !strings.Contains(strings.ToLower(ex.ID+" "+ex.Description+" "+strings.Join(ex.Tags, " ")), strings.ToLower(q.Text)) {
			continue
		}
		out = append(out, ex)
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (a fakeExamples) Get(_ context.Context, id string) (*domain.SavedExample, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	if i := a.find(id); i >= 0 {
		c := a.st.examples[i]
		return &c, nil
	}
	return nil, errs.New(errs.ExampleNotFound, "example %q not found", id).WithDetail("id", id)
}

func (a fakeExamples) Create(_ context.Context, ex domain.SavedExample) (*domain.SavedExample, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	if ex.ID == "" || ex.Operation == "" {
		return nil, errs.New(errs.Invalid, "example needs an id and an operation")
	}
	if a.find(ex.ID) >= 0 {
		return nil, errs.New(errs.Conflict, "example %q already exists", ex.ID).WithDetail("id", ex.ID)
	}
	fakeExampleDefaults(&ex)
	a.st.examples = append(a.st.examples, ex)
	c := ex
	return &c, nil
}

func (a fakeExamples) Update(_ context.Context, ex domain.SavedExample) (*domain.SavedExample, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	i := a.find(ex.ID)
	if i < 0 {
		return nil, errs.New(errs.ExampleNotFound, "example %q not found", ex.ID).WithDetail("id", ex.ID)
	}
	ex.Created = a.st.examples[i].Created
	fakeExampleDefaults(&ex)
	ex.Updated = time.Now()
	a.st.examples[i] = ex
	c := ex
	return &c, nil
}

func (a fakeExamples) Delete(_ context.Context, id string) error {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	i := a.find(id)
	if i < 0 {
		return errs.New(errs.ExampleNotFound, "example %q not found", id).WithDetail("id", id)
	}
	a.st.examples = append(a.st.examples[:i], a.st.examples[i+1:]...)
	return nil
}

func (a fakeExamples) FromRun(_ context.Context, req engine.ExampleFromRun) (*domain.SavedExample, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	var run *domain.Run
	for i := range a.st.runs {
		if a.st.runs[i].ID == req.RunID {
			run = &a.st.runs[i]
		}
	}
	if run == nil {
		return nil, errs.New(errs.RunNotFound, "run %q not found", req.RunID).WithDetail("id", req.RunID)
	}
	if req.ID == "" {
		return nil, errs.New(errs.Invalid, "example id is required")
	}
	if a.find(req.ID) >= 0 {
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
	ex := domain.SavedExample{ID: req.ID, Operation: step.Operation, Description: req.Description, Scope: req.Scope, Tags: req.Tags,
		Verified: &domain.ExampleVerified{Env: run.Environment, RunID: run.ID, StepID: step.StepID, At: time.Now(), Source: req.Source}}
	if step.Request != nil {
		ex.Body = step.Request.Body
		ex.Headers = map[string]string{}
		for k, v := range step.Request.Headers {
			if !strings.EqualFold(k, "Authorization") {
				ex.Headers[k] = v
			}
		}
	}
	if step.Response != nil {
		ex.Expect = &domain.ExampleExpect{Status: step.Response.Status, Body: step.Response.Body}
	}
	fakeExampleDefaults(&ex)
	a.st.examples = append(a.st.examples, ex)
	c := ex
	return &c, nil
}

func (a fakeExamples) ForOperations(_ context.Context, operationIDs []string, limit int) ([]domain.SavedExample, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	want := map[string]bool{}
	for _, id := range operationIDs {
		want[id] = true
	}
	var out []domain.SavedExample
	for _, ex := range a.st.examples {
		if want[ex.Operation] {
			out = append(out, ex)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		vi, vj := out[i].Verified != nil, out[j].Verified != nil
		if vi != vj {
			return vi
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (a fakeExamples) Reindex(context.Context) error { return nil }

func fakeExampleDefaults(ex *domain.SavedExample) {
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

// --- binding and flow tiers (PLAN §7b) --------------------------------

func (a fakeServices) Bind(_ context.Context, name, path string) (*domain.Service, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.services {
		if a.st.services[i].Name != name {
			continue
		}
		team := a.st.services[i].Source
		if b := a.st.services[i].Binding; b != nil && b.Team != nil {
			team = *b.Team
		}
		a.st.services[i].Source = domain.Source{Kind: domain.SourceLocal, Path: path}
		a.st.services[i].PackageDir = path
		a.st.services[i].Binding = &domain.ServiceBinding{
			Mode: domain.BindingLocal, Team: &team, Local: &domain.LocalCheckout{Path: path}, Writable: true,
		}
		c := a.st.services[i]
		return &c, nil
	}
	return nil, errs.New(errs.ServiceNotFound, "service %q not found", name)
}

func (a fakeServices) Unbind(_ context.Context, name string) (*domain.Service, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.services {
		if a.st.services[i].Name != name {
			continue
		}
		if b := a.st.services[i].Binding; b != nil && b.Team != nil {
			a.st.services[i].Source = *b.Team
			a.st.services[i].PackageDir = b.Team.Path
		}
		a.st.services[i].Binding = fakeBindingFor(a.st.services[i].Source)
		c := a.st.services[i]
		return &c, nil
	}
	return nil, errs.New(errs.ServiceNotFound, "service %q not found", name)
}

func (a fakeServices) Binding(_ context.Context, name string) (*engine.BindingInfo, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for _, s := range a.st.services {
		if s.Name != name {
			continue
		}
		b := s.Binding
		if b == nil {
			b = fakeBindingFor(s.Source)
		}
		return &engine.BindingInfo{Service: name, Binding: *b}, nil
	}
	return nil, errs.New(errs.ServiceNotFound, "service %q not found", name)
}

func fakeBindingFor(src domain.Source) *domain.ServiceBinding {
	if src.Kind == domain.SourceGit {
		s := src
		return &domain.ServiceBinding{Mode: domain.BindingTeam, Team: &s}
	}
	return &domain.ServiceBinding{Mode: domain.BindingLocal, Local: &domain.LocalCheckout{Path: src.Path}, Writable: true}
}

func (a fakeFlows) CreateIn(ctx context.Context, yamlSrc string, opts engine.CreateFlowOptions) (*domain.Flow, error) {
	kind := opts.OwnerKind
	if kind == "" {
		kind = domain.FlowOwnerLocal
	}
	created, err := a.Create(ctx, yamlSrc, opts.Path)
	if err != nil {
		return nil, err
	}
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.flows {
		if a.st.flows[i].ID == created.ID {
			a.st.flows[i].OwnerKind, a.st.flows[i].OwnerID = kind, opts.OwnerID
			if kind == domain.FlowOwnerLocal {
				a.st.flows[i].Path = filepath.Join(domain.LocalDir, domain.FlowsDir, filepath.Base(a.st.flows[i].Path))
			}
			c := a.st.flows[i]
			return &c, nil
		}
	}
	return created, nil
}

func (a fakeFlows) Rescope(_ context.Context, id string, ownerKind, ownerID string) (*domain.Flow, error) {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i := range a.st.flows {
		if a.st.flows[i].ID == id {
			a.st.flows[i].OwnerKind, a.st.flows[i].OwnerID = ownerKind, ownerID
			c := a.st.flows[i]
			return &c, nil
		}
	}
	return nil, errs.New(errs.FlowNotFound, "flow %q not found", id)
}
