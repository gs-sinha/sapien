package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/gs-sinha/sapien/internal/catalog"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/expr"
	"github.com/gs-sinha/sapien/internal/flow"
	"github.com/gs-sinha/sapien/internal/workspace"
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
// Workspace- and local-owned flows already carry an absolute Path
// (reindexOwnerFlows always writes one); service-owned flows carry a Path
// relative to the service's API package directory (registry.scanFlows's
// convention), so it is joined against the service's resolved PackageDir.
func (l *Local) resolveFlowPath(ctx context.Context, fs *domain.FlowSummary) (string, error) {
	if filepath.IsAbs(fs.Path) {
		return fs.Path, nil
	}
	switch fs.OwnerKind {
	case domain.FlowOwnerService:
		svc, err := l.cat.GetService(ctx, fs.OwnerID)
		if err != nil {
			return "", err
		}
		return filepath.Join(svc.PackageDir, fs.Path), nil
	case domain.FlowOwnerLocal:
		return filepath.Join(workspace.LocalDir(l.ws), fs.Path), nil
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

// Create is the workspace-tier shorthand for CreateIn: it exists so the
// callers and tests written before flows had tiers keep meaning exactly
// what they did (a file under <workspace>/flows), while every new caller
// goes through CreateIn and gets the local tier by default.
func (f *flowAPI) Create(ctx context.Context, yamlSrc string, path string) (*domain.Flow, error) {
	return f.CreateIn(ctx, yamlSrc, engine.CreateFlowOptions{Path: path, OwnerKind: domain.FlowOwnerWorkspace})
}

// CreateIn validates yamlSrc, resolves the tier opts names (local when
// empty: a flow is written and run on this machine first, and promoted
// once it earns it), defaults the path (flow.DefaultPath under that tier's
// flows directory) when opts.Path == "", refuses to overwrite an existing
// file or to reuse an id another tier already holds, writes it, reindexes
// that tier, and emits flow.changed.
//
// A non-empty path is always interpreted relative to the tier's flows
// directory (docs/flows.md's "Editing flows" section, PLAN §37, and
// docs/feedback/2026-09-05-41-step-flow-session.md item 5: "path is
// documented as relative to the flows directory; it's actually relative to
// the workspace root" -- list_flows silently never indexed the result). An
// absolute path, a path containing a ".." segment, or one that would
// resolve outside that directory is rejected with errs.Invalid naming the
// directory, rather than silently writing somewhere list_flows will never
// look.
//
// The tier is resolved before the source is validated: "you cannot write
// here at all" (an unknown service, or one read from a managed clone) is
// cheaper to learn than a list of diagnostics for a file that was never
// going to be saved.
func (f *flowAPI) CreateIn(ctx context.Context, yamlSrc string, opts engine.CreateFlowOptions) (*domain.Flow, error) {
	l := f.l
	ownerKind, ownerID := normalizeFlowOwner(opts.OwnerKind, opts.OwnerID)
	flowsDir, err := l.flowTierDir(ctx, ownerKind, ownerID)
	if err != nil {
		return nil, err
	}

	v := flow.NewValidator(&flowCatalogAdapter{cat: l.cat}, flow.WithExampleResolver(newFlowExampleResolver(l.Examples())))
	parsed, result := v.ValidateSource(ctx, yamlSrc)
	if !result.Valid {
		return nil, flowInvalidErr(result)
	}

	path := opts.Path
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
		if id == "" {
			id = flowIDFromPath(path)
		}
	}

	if _, err := os.Stat(path); err == nil {
		return nil, errs.New(errs.Conflict, "a flow already exists at %s", path)
	} else if !os.IsNotExist(err) {
		return nil, errs.Wrap(errs.Internal, err, "checking existing flow file %s", path)
	}
	// Ids are unique across tiers: the catalog keys flows by id alone, and
	// get_flow/run_flow address a flow by nothing else. Refusing here, before
	// the file exists, is what keeps the file and the index in step -- the
	// reindex below would otherwise fail on the duplicate row and leave a
	// file on disk that nothing lists.
	if existing, gerr := l.cat.GetFlowSummary(ctx, id); gerr == nil && existing != nil {
		return nil, errs.New(errs.Conflict, "flow %q already exists in the %s tier at %s", id, describeFlowOwner(existing.OwnerKind, existing.OwnerID), existing.Path).
			WithHint("choose another id, or move the existing flow with Rescope (`sapien flow promote`) instead of creating a second one")
	}

	if err := flow.Save(path, yamlSrc); err != nil {
		return nil, err
	}
	saved, err := flow.ParseFile(path)
	if err != nil {
		return nil, err
	}
	saved.OwnerKind, saved.OwnerID = ownerKind, ownerID

	if err := l.reindexOwnerFlows(ctx, ownerKind, ownerID, flowsDir); err != nil {
		return nil, err
	}
	l.emit(domain.EventFlowChanged, flow.Summary(saved))
	materialized := materializeFlow(ctx, l, saved)
	noteFlowOperationUse(ctx, l, materialized)
	return materialized, nil
}

// Rescope moves flow id to another tier, keeping its path relative to the
// tier's flows directory (a flow at flows/sub/x.flow.yaml lands at
// local/flows/sub/x.flow.yaml), reindexes the tier it left and the one it
// joined, re-homes its flow-scoped memories, and emits flow.changed. The
// file is moved, not rewritten: the developer's own formatting and comments
// survive promotion, and the flow's id does not change, so nothing that
// referenced it has to.
//
// The memories move after both reindexes, and through the memory store's
// own Update, because that is the one place the "a flow-scoped memory lives
// with its flow" rule is implemented (memory.Locator.flowDir consults the
// catalog for the flow's owner): the store sees an unchanged memory whose
// resolved directory is now different, writes the file there, and removes
// the old one. A memory that fails to move is logged and skipped rather
// than failing the call: the flow has already moved, and a half-done
// rescope that reports failure would be harder to recover from than a
// memory that Reindex or the next edit re-homes.
func (f *flowAPI) Rescope(ctx context.Context, id string, ownerKind, ownerID string) (*domain.Flow, error) {
	l := f.l
	existing, err := l.cat.GetFlowSummary(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, errs.New(errs.FlowNotFound, "flow %q not found", id)
	}
	oldPath, err := l.resolveFlowPath(ctx, existing)
	if err != nil {
		return nil, err
	}

	ownerKind, ownerID = normalizeFlowOwner(ownerKind, ownerID)
	targetDir, err := l.flowTierDir(ctx, ownerKind, ownerID)
	if err != nil {
		return nil, err
	}
	if existing.OwnerKind == ownerKind && existing.OwnerID == ownerID {
		return f.Get(ctx, id)
	}
	if existing.OwnerKind == domain.FlowOwnerService && l.readOnlyServices[existing.OwnerID] {
		// The old file cannot be removed from a managed clone; the next
		// sync would resurrect it anyway (git reset --hard), leaving the
		// same id in two tiers.
		return nil, readOnlyFlowErr(existing.OwnerID)
	}

	oldDir := ownerFlowsDir(l, existing.OwnerKind, existing.OwnerID)
	rel, relErr := filepath.Rel(oldDir, oldPath)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		rel = filepath.Base(oldPath)
	}
	newPath := filepath.Join(targetDir, rel)
	if _, err := os.Stat(newPath); err == nil {
		return nil, errs.New(errs.Conflict, "a flow already exists at %s", newPath).
			WithHint("remove or rename the file at the destination first; Rescope never overwrites")
	} else if !os.IsNotExist(err) {
		return nil, errs.Wrap(errs.Internal, err, "checking destination %s", newPath)
	}

	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return nil, errs.Wrap(errs.Internal, err, "creating %s", filepath.Dir(newPath))
	}
	if err := moveFile(oldPath, newPath); err != nil {
		return nil, err
	}

	if err := l.reindexOwnerFlows(ctx, existing.OwnerKind, existing.OwnerID, oldDir); err != nil {
		return nil, err
	}
	if err := l.reindexOwnerFlows(ctx, ownerKind, ownerID, targetDir); err != nil {
		return nil, err
	}
	l.rehomeFlowMemories(ctx, id)

	moved, err := flow.ParseFile(newPath)
	if err != nil {
		return nil, err
	}
	moved.OwnerKind, moved.OwnerID = ownerKind, ownerID
	l.emit(domain.EventFlowChanged, flow.Summary(moved))
	return materializeFlow(ctx, l, moved), nil
}

// rehomeFlowMemories rewrites every flow-scoped memory of flowID through
// the memory store so its file follows the flow's (new) owner; see Rescope.
func (l *Local) rehomeFlowMemories(ctx context.Context, flowID string) {
	mems, err := l.memStore.List(ctx, domain.MemoryQuery{Scope: domain.ScopeFlow, Flow: flowID, Limit: 10000})
	if err != nil {
		l.logger.Warn("listing flow memories to move with the flow failed", "flow", flowID, "error", err)
		return
	}
	for _, m := range mems {
		if m.Subject.Flow != flowID {
			continue
		}
		updated, err := l.memStore.Update(ctx, m)
		if err != nil {
			l.logger.Warn("moving a flow memory with its flow failed", "flow", flowID, "memory", m.ID, "error", err)
			continue
		}
		l.emit(domain.EventMemoryChanged, *updated)
	}
}

// moveFile renames src to dst, falling back to copy-then-remove when the
// two are on different filesystems (a service checkout on another volume
// than the workspace), the one case rename cannot serve.
func moveFile(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return errs.Wrap(errs.Internal, err, "moving %s to %s", src, dst)
	}
	in, err := os.Open(src)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "reading %s", src)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "creating %s", dst)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return errs.Wrap(errs.Internal, err, "copying %s to %s", src, dst)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return errs.Wrap(errs.Internal, err, "closing %s", dst)
	}
	if err := os.Remove(src); err != nil {
		return errs.Wrap(errs.Internal, err, "removing %s after copying it to %s", src, dst)
	}
	return nil
}

// normalizeFlowOwner applies the tier defaults: an empty kind is the local
// tier, and only the service tier carries an owner id.
func normalizeFlowOwner(ownerKind, ownerID string) (string, string) {
	if ownerKind == "" {
		ownerKind = domain.FlowOwnerLocal
	}
	if ownerKind != domain.FlowOwnerService {
		ownerID = ""
	}
	return ownerKind, ownerID
}

// flowTierDir resolves the flows directory a flow may be written into for
// (ownerKind, ownerID), refusing a tier that cannot take a write: an
// unknown kind, the service tier without a service name or with one the
// catalog does not know, or a service read from a managed git clone (its
// package is reset on every sync, so a flow written there would be lost;
// the hint says how to bind a checkout instead). The local tier is created,
// self-ignoring, on first use.
func (l *Local) flowTierDir(ctx context.Context, ownerKind, ownerID string) (string, error) {
	switch ownerKind {
	case domain.FlowOwnerLocal:
		if err := workspace.EnsureLocalDir(l.ws); err != nil {
			return "", err
		}
		return workspace.LocalFlowsDir(l.ws), nil
	case domain.FlowOwnerWorkspace:
		return filepath.Join(l.ws.Dir, domain.FlowsDir), nil
	case domain.FlowOwnerService:
		if ownerID == "" {
			return "", errs.New(errs.Invalid, "the service tier needs owner_id: the service whose api/flows the flow belongs in").
				WithHint("pass owner_id, or use the workspace tier")
		}
		svc, err := l.cat.GetService(ctx, ownerID)
		if err != nil {
			return "", err
		}
		if l.readOnlyServices[ownerID] {
			return "", readOnlyFlowErr(ownerID)
		}
		return filepath.Join(svc.PackageDir, domain.FlowsDir), nil
	default:
		return "", errs.New(errs.Invalid, "unknown flow tier %q; want %s, %s, or %s", ownerKind, domain.FlowOwnerLocal, domain.FlowOwnerWorkspace, domain.FlowOwnerService)
	}
}

// readOnlyFlowErr is the refusal for writing a flow into a service read
// from a managed clone; it mirrors memory.Locator's wording so an agent
// sees the same rule and the same way out for every kind of knowledge.
func readOnlyFlowErr(service string) error {
	return errs.New(errs.Invalid, "service %q is read from a managed git clone that Sapien resets on every sync, so a flow cannot be written into it", service).
		WithDetail("service", service).
		WithHint("bind a local checkout with `sapien service bind " + service + " <path>` so the flow rides your own branch, or use the workspace tier")
}

// describeFlowOwner names a tier for a message: "local", "workspace", or
// "service order-service".
func describeFlowOwner(ownerKind, ownerID string) string {
	if ownerKind == domain.FlowOwnerService && ownerID != "" {
		return ownerKind + " " + ownerID
	}
	return ownerKind
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
	if existing.OwnerKind == domain.FlowOwnerService && l.readOnlyServices[existing.OwnerID] {
		return nil, readOnlyFlowErr(existing.OwnerID)
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

// Delete removes a local- or workspace-owned flow's file and reindexes
// that tier; a service-owned flow refuses with errs.Invalid (its canonical
// copy lives in the service's repo).
func (f *flowAPI) Delete(ctx context.Context, id string) error {
	l := f.l
	existing, err := l.cat.GetFlowSummary(ctx, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return errs.New(errs.FlowNotFound, "flow %q not found", id)
	}
	if existing.OwnerKind == domain.FlowOwnerService {
		return errs.New(errs.Invalid, "flow %q is owned by service %q; edit the service repo", id, existing.OwnerID)
	}

	existingPath, err := l.resolveFlowPath(ctx, existing)
	if err != nil {
		return err
	}
	if err := os.Remove(existingPath); err != nil && !os.IsNotExist(err) {
		return errs.Wrap(errs.Internal, err, "removing flow file %s", existingPath)
	}
	if err := l.reindexOwnerFlows(ctx, existing.OwnerKind, existing.OwnerID, ownerFlowsDir(l, existing.OwnerKind, existing.OwnerID)); err != nil {
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

// ownerFlowsDir resolves the flows directory for a flow owner: the local
// tier's flows dir for "local", the workspace's for "workspace", or
// <service package dir>/flows for "service". Falls back to the workspace
// flows dir if the service can't be resolved (defensive; reindexOwnerFlows
// then simply finds nothing to index).
func ownerFlowsDir(l *Local, ownerKind, ownerID string) string {
	switch ownerKind {
	case domain.FlowOwnerService:
		if svc, err := l.cat.GetService(context.Background(), ownerID); err == nil {
			return filepath.Join(svc.PackageDir, domain.FlowsDir)
		}
	case domain.FlowOwnerLocal:
		return workspace.LocalFlowsDir(l.ws)
	}
	return filepath.Join(l.ws.Dir, domain.FlowsDir)
}

// reindexWorkspaceFlows rebuilds the catalog's flow rows for both tiers
// the workspace directory holds -- workspace-owned, from <workspace>/flows,
// and local-owned, from <workspace>/local/flows. The two go together
// because the file watcher reports either directory as the one "flows"
// area (registry.Watcher), and the open-time staleness check keys on the
// same area; a caller that knows which tier changed uses reindexOwnerFlows
// directly.
func (l *Local) reindexWorkspaceFlows(ctx context.Context) error {
	if err := l.reindexOwnerFlows(ctx, domain.FlowOwnerWorkspace, "", filepath.Join(l.ws.Dir, domain.FlowsDir)); err != nil {
		return err
	}
	return l.reindexLocalFlows(ctx)
}

// reindexLocalFlows rebuilds the catalog's local-owned flow rows from
// every *.flow.yaml file under <workspace>/local/flows.
func (l *Local) reindexLocalFlows(ctx context.Context) error {
	return l.reindexOwnerFlows(ctx, domain.FlowOwnerLocal, "", workspace.LocalFlowsDir(l.ws))
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
(personal scope: SQLite only). A flow-scoped memory lives with its flow's
tier -- ` + "`<workspace>/local/memories`" + ` for a local flow, the workspace's
or the service's memories directory otherwise -- and moves when the flow
is promoted.

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
