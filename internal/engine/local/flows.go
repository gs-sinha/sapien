package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/catalog"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/expr"
	"github.com/gs-sinha/sapien/internal/flow"
)

// flowAPI implements engine.FlowAPI over a Local (PLAN §8, §23.1).
type flowAPI struct{ l *Local }

var _ engine.FlowAPI = (*flowAPI)(nil)

// flowCatalogAdapter adapts *catalog.Catalog to flow.Catalog, the small
// read-only surface the flow validator needs.
type flowCatalogAdapter struct{ cat *catalog.Catalog }

var _ flow.Catalog = (*flowCatalogAdapter)(nil)

func (a *flowCatalogAdapter) Operation(ctx context.Context, id string) (*domain.Operation, bool) {
	op, err := a.cat.GetOperation(ctx, id)
	if err != nil {
		return nil, false
	}
	return op, true
}

func (a *flowCatalogAdapter) Suggest(ctx context.Context, ref string, n int) []string {
	ids, _ := a.cat.SuggestOperationIDs(ctx, ref, n)
	return ids
}

func (a *flowCatalogAdapter) Fields(ctx context.Context, operationID string) []domain.Field {
	fields, _ := a.cat.Fields(ctx, operationID)
	return fields
}

// flowExampleResolver adapts engine.ExampleAPI (l.Examples()) to
// flow.ExampleResolver, the small read-only surface Materialize/Validate
// need to resolve a step's `example:` reference (PLAN §34b; mirrors
// flowCatalogAdapter above for operations).
type flowExampleResolver struct{ examples engine.ExampleAPI }

var _ flow.ExampleResolver = (*flowExampleResolver)(nil)

func newFlowExampleResolver(examples engine.ExampleAPI) *flowExampleResolver {
	return &flowExampleResolver{examples: examples}
}

// Example looks id up through ExampleAPI.Get. flow.ExampleResolver has no
// error channel of its own (mirroring flow.Catalog.Operation's bool-only
// shape), so any error -- errs.ExampleNotFound, or, until another agent
// wires internal/example into Local, the errs.NotImplemented the stub in
// examples.go returns today -- is reported as simply "unknown".
func (r *flowExampleResolver) Example(ctx context.Context, id string) (*domain.SavedExample, bool) {
	ex, err := r.examples.Get(ctx, id)
	if err != nil {
		return nil, false
	}
	return ex, true
}

// SuggestExamples lists every known example (List with no filter) and
// ranks their ids against ref with flow.NearestSuggestions, the same
// fuzzy-matching helper behind UNKNOWN_OPERATION's suggestions.
func (r *flowExampleResolver) SuggestExamples(ctx context.Context, ref string, n int) []string {
	list, err := r.examples.List(ctx, domain.ExampleQuery{})
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(list))
	for _, ex := range list {
		ids = append(ids, ex.ID)
	}
	return flow.NearestSuggestions(ref, ids, n)
}

// materializeFlow expands f's `example:` steps against the workspace's
// saved examples (flowExampleResolver over l.Examples()) so Get, Create,
// Update, and a run driven through Flows().Get (RunFlow -> Get -> runFlow)
// all see the same expanded call/input/body/headers (PLAN §34b). Diagnostics
// are intentionally dropped here: Create/Update already surface them as a
// FlowInvalid error at save time via Validate's own Materialize pass, Get
// has no diagnostics channel to report them through, and a step whose
// `call` stays unresolved fails loudly enough at run time through the
// runner's own pre-flight check -- so a stale reference degrades a display
// or a run's outcome rather than making an otherwise-fine file unreadable.
func materializeFlow(ctx context.Context, l *Local, f *domain.Flow) *domain.Flow {
	materialized, _ := flow.Materialize(ctx, f, newFlowExampleResolver(l.Examples()))
	return materialized
}

// List returns every flow (catalog.ListFlows("", "")), filtered by a
// case-insensitive substring match on id/name/tags when query != "".
func (f *flowAPI) List(ctx context.Context, query string) ([]domain.FlowSummary, error) {
	all, err := f.l.cat.ListFlows(ctx, "", "")
	if err != nil {
		return nil, err
	}
	if query == "" {
		return all, nil
	}
	q := strings.ToLower(query)
	out := make([]domain.FlowSummary, 0, len(all))
	for _, fs := range all {
		if strings.Contains(strings.ToLower(fs.ID), q) || strings.Contains(strings.ToLower(fs.Name), q) {
			out = append(out, fs)
			continue
		}
		for _, tag := range fs.Tags {
			if strings.Contains(strings.ToLower(tag), q) {
				out = append(out, fs)
				break
			}
		}
	}
	return out, nil
}

// Get finds id's summary in the catalog, then parses its file from disk.
func (f *flowAPI) Get(ctx context.Context, id string) (*domain.Flow, error) {
	l := f.l
	fs, err := l.cat.GetFlowSummary(ctx, id)
	if err != nil {
		return nil, err
	}
	if fs == nil {
		return nil, errs.New(errs.FlowNotFound, "flow %q not found", id)
	}
	path, err := l.resolveFlowPath(ctx, fs)
	if err != nil {
		return nil, err
	}
	parsed, err := flow.ParseFile(path)
	if err != nil {
		return nil, err
	}
	parsed.OwnerKind, parsed.OwnerID = fs.OwnerKind, fs.OwnerID
	return materializeFlow(ctx, l, parsed), nil
}

// resolveFlowPath resolves a FlowSummary's Path to an absolute path.
// Workspace-owned flows already carry an absolute Path (reindexOwnerFlows
// always writes one); service-owned flows carry a Path relative to the
// service's API package directory (registry.scanFlows's convention), so it
// is joined against the service's resolved PackageDir.
func (l *Local) resolveFlowPath(ctx context.Context, fs *domain.FlowSummary) (string, error) {
	if filepath.IsAbs(fs.Path) {
		return fs.Path, nil
	}
	if fs.OwnerKind == "service" {
		svc, err := l.cat.GetService(ctx, fs.OwnerID)
		if err != nil {
			return "", err
		}
		return filepath.Join(svc.PackageDir, fs.Path), nil
	}
	return filepath.Join(l.ws.Dir, fs.Path), nil
}

// Parse turns YAML into a Flow without validating references.
func (f *flowAPI) Parse(ctx context.Context, yamlSrc string) (*domain.Flow, error) {
	return flow.Parse(yamlSrc)
}

// Validate checks references, bindings, and expressions against the
// catalog, and every step's `example:` reference against the workspace's
// saved examples.
func (f *flowAPI) Validate(ctx context.Context, yamlSrc string) (*domain.ValidationResult, error) {
	v := flow.NewValidator(&flowCatalogAdapter{cat: f.l.cat}, flow.WithExampleResolver(newFlowExampleResolver(f.l.Examples())))
	_, result := v.ValidateSource(ctx, yamlSrc)
	return result, nil
}

// Create validates yamlSrc, defaults path (flow.DefaultPath under the
// workspace flows directory) when path == "", refuses to overwrite an
// existing file, writes it, reindexes workspace flows, and emits
// flow.changed.
//
// A non-empty path is always interpreted relative to the workspace's flows
// directory (docs/flows.md's "Editing flows" section, PLAN §37, and
// docs/feedback/2026-09-05-41-step-flow-session.md item 5: "path is
// documented as relative to the flows directory; it's actually relative to
// the workspace root" -- list_flows silently never indexed the result). An
// absolute path, a path containing a ".." segment, or one that would
// resolve outside that directory is rejected with errs.Invalid naming the
// directory, rather than silently writing somewhere list_flows will never
// look.
func (f *flowAPI) Create(ctx context.Context, yamlSrc string, path string) (*domain.Flow, error) {
	l := f.l
	v := flow.NewValidator(&flowCatalogAdapter{cat: l.cat}, flow.WithExampleResolver(newFlowExampleResolver(l.Examples())))
	parsed, result := v.ValidateSource(ctx, yamlSrc)
	if !result.Valid {
		return nil, flowInvalidErr(result)
	}

	flowsDir := filepath.Join(l.ws.Dir, domain.FlowsDir)
	id := parsed.ID
	if path == "" {
		if id == "" {
			return nil, errs.New(errs.Invalid, "flow has no `id:`; set one, or pass an explicit path")
		}
		path = flow.DefaultPath(flowsDir, id)
	} else {
		resolved, verr := resolveFlowsPath(flowsDir, path)
		if verr != nil {
			return nil, verr
		}
		path = resolved
	}

	if _, err := os.Stat(path); err == nil {
		return nil, errs.New(errs.Conflict, "a flow already exists at %s", path)
	} else if !os.IsNotExist(err) {
		return nil, errs.Wrap(errs.Internal, err, "checking existing flow file %s", path)
	}

	if err := flow.Save(path, yamlSrc); err != nil {
		return nil, err
	}
	saved, err := flow.ParseFile(path)
	if err != nil {
		return nil, err
	}
	saved.OwnerKind = "workspace"

	if err := l.reindexWorkspaceFlows(ctx); err != nil {
		return nil, err
	}
	l.emit(domain.EventFlowChanged, flow.Summary(saved))
	materialized := materializeFlow(ctx, l, saved)
	noteFlowOperationUse(ctx, l, materialized)
	return materialized, nil
}

// Update requires id to already exist and yamlSrc's own id (or, if unset,
// the id implied by the existing file's path) to match it, validates the
// new source, saves it over the existing file, reindexes the flow's owner
// (workspace or service), and emits flow.changed.
func (f *flowAPI) Update(ctx context.Context, id string, yamlSrc string) (*domain.Flow, error) {
	l := f.l
	existing, err := l.cat.GetFlowSummary(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, errs.New(errs.FlowNotFound, "flow %q not found", id)
	}

	existingPath, err := l.resolveFlowPath(ctx, existing)
	if err != nil {
		return nil, err
	}

	newID := flowIDOf(yamlSrc, existingPath)
	if newID != id {
		return nil, errs.New(errs.Invalid, "flow id `%s` does not match `%s`; Update cannot rename a flow", newID, id)
	}

	v := flow.NewValidator(&flowCatalogAdapter{cat: l.cat}, flow.WithExampleResolver(newFlowExampleResolver(l.Examples())))
	_, result := v.ValidateSource(ctx, yamlSrc)
	if !result.Valid {
		return nil, flowInvalidErr(result)
	}

	if err := flow.Save(existingPath, yamlSrc); err != nil {
		return nil, err
	}
	saved, err := flow.ParseFile(existingPath)
	if err != nil {
		return nil, err
	}
	saved.OwnerKind, saved.OwnerID = existing.OwnerKind, existing.OwnerID

	if err := l.reindexOwnerFlows(ctx, existing.OwnerKind, existing.OwnerID, ownerFlowsDir(l, existing.OwnerKind, existing.OwnerID)); err != nil {
		return nil, err
	}
	l.emit(domain.EventFlowChanged, flow.Summary(saved))
	materialized := materializeFlow(ctx, l, saved)
	noteFlowOperationUse(ctx, l, materialized)
	return materialized, nil
}

// noteFlowOperationUse records usage feedback (search ranking tuning task,
// part 2) for every distinct operation f's (already-materialized) steps
// call. Flow validation requires every step's Call to already be a
// canonical operation ID by the time Create/Update save the file (see
// flowCatalogAdapter.Operation, an exact GetOperation lookup, not a fuzzy
// ResolveOperation), so step.Call needs no further resolution here.
func noteFlowOperationUse(ctx context.Context, l *Local, f *domain.Flow) {
	if f == nil {
		return
	}
	seen := map[string]bool{}
	for _, step := range f.Steps {
		if step.Call == "" || seen[step.Call] {
			continue
		}
		seen[step.Call] = true
		noteOperationUse(ctx, l, step.Call)
	}
}

// Delete removes a workspace-owned flow's file and reindexes; a
// service-owned flow refuses with errs.Invalid (its canonical copy lives in
// the service's repo).
func (f *flowAPI) Delete(ctx context.Context, id string) error {
	l := f.l
	existing, err := l.cat.GetFlowSummary(ctx, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return errs.New(errs.FlowNotFound, "flow %q not found", id)
	}
	if existing.OwnerKind != "workspace" {
		return errs.New(errs.Invalid, "flow %q is owned by service %q; edit the service repo", id, existing.OwnerID)
	}

	existingPath, err := l.resolveFlowPath(ctx, existing)
	if err != nil {
		return err
	}
	if err := os.Remove(existingPath); err != nil && !os.IsNotExist(err) {
		return errs.Wrap(errs.Internal, err, "removing flow file %s", existingPath)
	}
	if err := l.reindexWorkspaceFlows(ctx); err != nil {
		return err
	}
	l.emit(domain.EventFlowChanged, *existing)
	return nil
}

// Reference returns the DSL reference text for agents (PLAN §23): sapien
// (what Sapien is and what it can do), flow, expressions, memory, or service
// (the api/ package layout).
func (f *flowAPI) Reference(ctx context.Context, topic string) (string, error) {
	switch topic {
	case "sapien":
		return sapienReferenceText, nil
	case "flow":
		return flow.Reference(), nil
	case "expressions":
		return expr.Reference(), nil
	case "memory":
		return memoryReferenceText, nil
	case "service":
		return serviceReferenceText, nil
	default:
		return "", errs.New(errs.Invalid, "unknown reference topic %q; want sapien, flow, memory, expressions, or service", topic)
	}
}

// flowInvalidErr builds the errs.FlowInvalid error Create/Update/RunFlowSource
// return for an invalid flow, carrying its diagnostics as a detail.
func flowInvalidErr(result *domain.ValidationResult) error {
	return errs.New(errs.FlowInvalid, "flow validation failed: %d problem(s)", len(result.Diagnostics)).
		WithDetail("diagnostics", result.Diagnostics)
}

// flowIDOf returns src's effective flow id: its own `id:` field if set,
// else the id implied by fallbackPath's file name (mirrors flow.ParseFile's
// default-id rule, PLAN §8: "default: filename stem").
func flowIDOf(src, fallbackPath string) string {
	f, err := flow.Parse(src)
	if err == nil && f.ID != "" {
		return f.ID
	}
	return flowIDFromPath(fallbackPath)
}

// flowIDFromPath derives a flow's default id from its file path: the base
// name with a ".flow.yaml"/".flow.yml" suffix removed.
func flowIDFromPath(path string) string {
	base := filepath.Base(path)
	for _, suf := range []string{".flow.yaml", ".flow.yml"} {
		if strings.HasSuffix(base, suf) {
			return strings.TrimSuffix(base, suf)
		}
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// ownerFlowsDir resolves the flows directory for a flow owner: the
// workspace's flows dir for "workspace", or <service package dir>/flows for
// "service". Falls back to the workspace flows dir if the service can't be
// resolved (defensive; reindexOwnerFlows then simply finds nothing to index).
func ownerFlowsDir(l *Local, ownerKind, ownerID string) string {
	if ownerKind == "service" {
		if svc, err := l.cat.GetService(context.Background(), ownerID); err == nil {
			return filepath.Join(svc.PackageDir, "flows")
		}
	}
	return filepath.Join(l.ws.Dir, domain.FlowsDir)
}

// reindexWorkspaceFlows rebuilds the catalog's workspace-owned flow rows
// from every *.flow.yaml file under <workspace>/flows.
func (l *Local) reindexWorkspaceFlows(ctx context.Context) error {
	return l.reindexOwnerFlows(ctx, "workspace", "", filepath.Join(l.ws.Dir, domain.FlowsDir))
}

// reindexOwnerFlows rebuilds the catalog's flow rows owned by
// (ownerKind, ownerID) from every *.flow.yaml file anywhere under dir, at
// any depth -- a flow saved at, say, dir/sub/x.flow.yaml is found the same
// as one directly under dir, so a path with subdirectories (create_flow's
// `path`, PLAN §37) is never silently un-indexed. A file that fails to
// parse is skipped (PLAN §17: "parse errors keep the last good catalog"
// for that one file) rather than failing the whole reindex; a missing dir
// indexes as zero flows.
func (l *Local) reindexOwnerFlows(ctx context.Context, ownerKind, ownerID, dir string) error {
	var relPaths []string
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), domain.FlowFileSuffix) {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		relPaths = append(relPaths, rel)
		return nil
	})
	if walkErr != nil {
		if !os.IsNotExist(walkErr) {
			return errs.Wrap(errs.Internal, walkErr, "reading flows directory %s", dir)
		}
		relPaths = nil
	}
	sort.Strings(relPaths)

	// A file that fails to parse must not cost the flow its index row: an
	// editor mid-save, a stray syntax error, or an older process reading a
	// newer grammar would otherwise make the flow vanish from list_flows
	// and every id-addressed call while its file sits on disk. Keep the
	// row the catalog already has for that path (or id) and say so.
	existing := map[string]domain.FlowSummary{}
	if prev, lerr := l.cat.ListFlows(ctx, ownerKind, ownerID); lerr == nil {
		for _, p := range prev {
			existing[p.Path] = p
			existing[p.ID] = p
		}
	}

	summaries := make([]domain.FlowSummary, 0, len(relPaths))
	for _, rel := range relPaths {
		path := filepath.Join(dir, rel)
		fl, err := flow.ParseFile(path)
		if err != nil {
			l.logger.Warn("flow file failed to parse; keeping its previous index entry", "path", path, "error", err)
			if kept, ok := existing[path]; ok {
				summaries = append(summaries, kept)
			} else if kept, ok := existing[flowIDFromPath(path)]; ok {
				summaries = append(summaries, kept)
			}
			continue
		}
		fl.OwnerKind, fl.OwnerID = ownerKind, ownerID
		// Materialize before summarizing so a step reaching its operation
		// only through `example:` still counts toward the catalog's
		// Operations list (used by search/context filtering by operation).
		sum := flow.Summary(materializeFlow(ctx, l, fl))
		if info, statErr := os.Stat(path); statErr == nil {
			sum.Updated = info.ModTime().UTC()
		} else {
			sum.Updated = time.Now().UTC()
		}
		summaries = append(summaries, sum)
	}

	return l.cat.UpsertFlows(ctx, ownerKind, ownerID, summaries)
}

// resolveFlowsPath validates rel (create_flow's/`sapien flow create`'s
// path) as a safe, indexable location inside flowsDir: not empty, not
// absolute, no ".." path segment (which also rules out ever resolving
// outside flowsDir, since flowsDir itself is a fixed absolute prefix), and
// the file name must end in domain.FlowFileSuffix (".flow.yaml") so
// reindexOwnerFlows (any depth under flowsDir, see above) actually finds
// it. Returns the resolved absolute path.
func resolveFlowsPath(flowsDir, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", errs.New(errs.Invalid, "path is required").
			WithHint(fmt.Sprintf("pass a path relative to %s", flowsDir))
	}
	if filepath.IsAbs(rel) {
		return "", errs.New(errs.Invalid, "path %q must be relative to the flows directory %s, not absolute", rel, flowsDir).
			WithHint(fmt.Sprintf("pass a path relative to %s, e.g. %q", flowsDir, filepath.Base(rel)))
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".." {
			return "", errs.New(errs.Invalid, "path %q must not contain \"..\"", rel).
				WithHint(fmt.Sprintf("use a path inside %s", flowsDir))
		}
	}
	if !strings.HasSuffix(rel, domain.FlowFileSuffix) {
		return "", errs.New(errs.Invalid, "path %q must end in %s so list_flows finds it", rel, domain.FlowFileSuffix).
			WithHint(fmt.Sprintf("use a name like my-flow%s", domain.FlowFileSuffix))
	}
	return filepath.Join(flowsDir, rel), nil
}

// memoryReferenceText is served by Reference("memory") -- a concise
// reference of the memory file format and subject keys this package writes
// inline (PLAN §10-11).
const memoryReferenceText = `# Memory reference

A memory is Markdown with YAML front matter: one file per memory, stored
under ` + "`<workspace>/memories/<id>.md`" + ` (workspace scope) or
` + "`<service>/api/memories/<id>.md`" + ` (service scope), or nowhere on disk
(personal scope: SQLite only).

## File format

` + "```" + `markdown
---
id: mem_01J8Z5K3W2RQ4X7M9N
type: semantic          # semantic | behavioral | testing | invariant | environment | gotcha | note (default)
scope: workspace         # personal | workspace | service | flow
subject:
  operation: rider-service.getRider
  field: response.200.body.qcomSkill
tags: [qcom, allocation]
source: { kind: user }   # user | agent{model,client} | run{run_id,step_id} | import{path} | documentation{file}
created: 2026-09-05T10:20:00Z
updated: 2026-09-05T10:20:00Z
status: active            # active | promoted | superseded | deprecated | disputed
---
qcomSkill indicates whether the rider is eligible for quick-commerce (QCOM)
orders. It does not indicate the rider is online or available.

Implications: QCOM allocation should only select riders with qcomSkill=true.
` + "```" + `

Only ` + "`id`" + `, ` + "`scope`" + `, ` + "`created`" + `, and the body are required; ` + "`type`" + ` defaults to
` + "`note`" + ` and ` + "`status`" + ` to ` + "`active`" + `. An "Implications:" paragraph in the body is a
convention, not schema.

## Subject keys

Any combination may be set; a memory may describe more than one thing.

| Key | Value | Notes |
|---|---|---|
| ` + "`service`" + ` | service name | |
| ` + "`operation`" + ` | operation ID | ` + "`<service>.<operationId>`" + ` |
| ` + "`field`" + ` | field path | requires ` + "`operation`" + ` or ` + "`schema`" + ` |
| ` + "`schema`" + ` | ` + "`<service>.ComponentName`" + ` | |
| ` + "`flow`" + ` / ` + "`step`" + ` | flow ID / flow ID + step ID | |
| ` + "`run`" + ` | run ID | kept as provenance even if the run is purged |
| ` + "`environment`" + ` | environment name | |
| ` + "`error`" + ` | ` + "`{operation, status, code}`" + ` | |
| ` + "`concept`" + ` | free string | lexical only |

A resolved subject snapshots ` + "`{method, path, operation_hash, resolved_at}`" + `; if it stops
resolving (e.g. the operation was removed), it is flagged ` + "`unresolved: true`" + `
rather than rewritten.

## Choosing a scope

Scope decides storage and sharing, not subject. Service scope is committed
under ` + "`<service>/api/memories`" + `: reviewed like any other change, and it
reaches everyone who clones that repo. Workspace scope lives under
` + "`<workspace>/memories`" + `; it has no git history unless the workspace itself
is a git repo, and it is invisible to teammates who don't share your
machine. That asymmetry, not what the memory is about, is the decision: a
workspace-scoped memory may still carry a ` + "`subject.service`" + `.

Ask: would this still be true in a fresh environment with empty databases?
Yes means service scope. No means workspace scope. A memory that mixes a
lasting contract fact ("this field means X") with local environment data
("staging is slow right now") belongs in neither scope as written; split
it into two memories instead.

## Promotion

A memory is a staging area, not the destination. ` + "`get_promotion_target(memory_id)`" + `
(MCP) and ` + "`sapien memory promote <id>`" + ` (CLI) locate where a stabilised
fact belongs: an OpenAPI file, line, and JSON pointer for a field or
operation, or a doc section under ` + "`api/docs/`" + ` for behavioral, testing,
and invariant memories. Promote once the knowledge has held up, not on
first capture, and consider it too when a new memory closely matches an
old one. The edit happens in the service's own repo, reviewed like any
other change; nothing here writes it automatically. Once applied, mark
the memory ` + "`status: promoted`" + `.
`
