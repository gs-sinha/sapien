package local

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/example"
	"github.com/gs-sinha/sapien/internal/flow"
	"github.com/gs-sinha/sapien/internal/spec"
)

// exampleAPI implements engine.ExampleAPI over internal/example (PLAN
// §34b): a saved example is validated against the catalog exactly like a
// one-step flow ("input"/"body" checked the same way, same diagnostic
// codes -- see internal/flow's Code* constants, reused here rather than
// duplicated), then delegated to the store for file + index writes.
type exampleAPI struct{ l *Local }

var _ engine.ExampleAPI = (*exampleAPI)(nil)

// List returns examples matching q. Workspace-tier results carry Shipped
// (PLAN §7b), from one read-only look at the workspace's git repository;
// service-tier results never do. See fillExampleShipped.
func (e *exampleAPI) List(ctx context.Context, q domain.ExampleQuery) ([]domain.SavedExample, error) {
	out, err := e.l.exStore.List(ctx, q)
	if err != nil {
		return nil, err
	}
	e.l.fillExampleShipped(ctx, out)
	return out, nil
}

func (e *exampleAPI) Get(ctx context.Context, id string) (*domain.SavedExample, error) {
	ex, err := e.l.exStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	list := []domain.SavedExample{*ex}
	e.l.fillExampleShipped(ctx, list)
	out := list[0]
	return &out, nil
}

// Create rejects a caller-supplied Verified (only FromRun sets it),
// validates ex against the catalog and the example file schema, and writes
// it via the store.
func (e *exampleAPI) Create(ctx context.Context, ex domain.SavedExample) (*domain.SavedExample, error) {
	if ex.Verified != nil {
		return nil, errs.New(errs.Invalid, "verified is set only by FromRun")
	}
	if err := e.validate(ctx, ex); err != nil {
		return nil, err
	}
	return e.l.exStore.Create(ctx, ex)
}

// Update validates ex the same way Create does (defaulting a blank
// Operation from the existing example first, mirroring the store's own
// "keep existing" leniency for Update) and delegates to the store, which
// moves the file if Scope changed.
func (e *exampleAPI) Update(ctx context.Context, ex domain.SavedExample) (*domain.SavedExample, error) {
	checked := ex
	if checked.Operation == "" {
		existing, err := e.l.exStore.Get(ctx, ex.ID)
		if err != nil {
			return nil, err
		}
		checked.Operation = existing.Operation
	}
	if err := e.validate(ctx, checked); err != nil {
		return nil, err
	}
	return e.l.exStore.Update(ctx, ex)
}

func (e *exampleAPI) Delete(ctx context.Context, id string) error {
	return e.l.exStore.Delete(ctx, id)
}

func (e *exampleAPI) ForOperations(ctx context.Context, operationIDs []string, limit int) ([]domain.SavedExample, error) {
	return e.l.exStore.ForOperations(ctx, operationIDs, limit)
}

func (e *exampleAPI) Reindex(ctx context.Context) error {
	_, err := e.l.exStore.Reindex(ctx)
	return err
}

// validate checks ex against the catalog (the operation must exist; input
// names and body presence are checked exactly like a flow step) and
// against the example file schema (spec.Example), collecting every problem
// found into one errs.Invalid, shaped like flow validation's
// Details["diagnostics"] (PLAN §23.1), rather than failing on the first
// check. A schema-only problem (e.g. an invalid id) is reported even when
// the operation doesn't exist, so a caller sees every problem at once.
func (e *exampleAPI) validate(ctx context.Context, ex domain.SavedExample) error {
	var diags []domain.Diagnostic

	switch {
	case ex.Operation == "":
		diags = append(diags, domain.Diagnostic{
			Code: flow.CodeUnknownOperation, Severity: domain.SeverityError,
			Message: "operation is required",
		})
	default:
		op, err := e.l.cat.GetOperation(ctx, ex.Operation)
		switch {
		case err == nil:
			diags = append(diags, checkExampleInputNames(ex, op)...)
			diags = append(diags, checkExampleBody(ex, op)...)
		case errs.Is(err, errs.OperationNotFound):
			diags = append(diags, unknownOperationDiagnostic(ex.Operation, err))
		default:
			return err
		}
	}

	schemaDiags, err := exampleSchemaDiagnostics(ex)
	if err != nil {
		return err
	}
	diags = append(diags, schemaDiags...)

	if hasErrorDiagnostic(diags) {
		return exampleInvalidErr(diags)
	}
	return nil
}

// unknownOperationDiagnostic builds the UNKNOWN_OPERATION diagnostic for an
// operation catalog.GetOperation could not find, including suggestions --
// catalog.GetOperation already computes them with the same fuzzy suggest
// the flow validator's Catalog adapter uses (catalog.SuggestOperationIDs),
// via Details["suggestions"] -- or falling back to the plain "unknown
// operation" message when none were found.
func unknownOperationDiagnostic(opID string, notFound error) domain.Diagnostic {
	var suggestions []string
	if e := errs.As(notFound); e != nil {
		if s, ok := e.Details["suggestions"].([]string); ok {
			suggestions = s
		}
	}
	msg := fmt.Sprintf("unknown operation `%s`", opID)
	if len(suggestions) > 0 {
		msg = fmt.Sprintf("unknown operation `%s`; did you mean `%s`?", opID, suggestions[0])
	}
	return domain.Diagnostic{
		Code: flow.CodeUnknownOperation, Severity: domain.SeverityError,
		Message: msg, Suggestions: suggestions,
	}
}

// checkExampleInputNames reports an UNKNOWN_INPUT_NAME diagnostic for every
// ex.Input key that isn't one of op's path/query/header parameters (header
// names compare case-insensitively; path and query compare exactly, same
// as flow's checkBinding).
func checkExampleInputNames(ex domain.SavedExample, op *domain.Operation) []domain.Diagnostic {
	if len(ex.Input) == 0 {
		return nil
	}
	keys := make([]string, 0, len(ex.Input))
	for k := range ex.Input {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var diags []domain.Diagnostic
	for _, k := range keys {
		if operationHasParam(op, k) {
			continue
		}
		diags = append(diags, domain.Diagnostic{
			Code: flow.CodeUnknownInputName, Severity: domain.SeverityError,
			Message:     fmt.Sprintf("unknown input name `%s` for operation `%s`", k, op.ID),
			Suggestions: paramSuggestions(op),
		})
	}
	return diags
}

// operationHasParam reports whether op declares a path, query, or header
// parameter named name.
func operationHasParam(op *domain.Operation, name string) bool {
	for _, p := range op.Params {
		switch p.In {
		case domain.InPath, domain.InQuery:
			if p.Name == name {
				return true
			}
		case domain.InHeader:
			if strings.EqualFold(p.Name, name) {
				return true
			}
		}
	}
	return false
}

// paramSuggestions renders op's declared parameters as "name (in)" strings,
// for an UNKNOWN_INPUT_NAME diagnostic's Suggestions (mirrors flow's
// paramSuggestionList).
func paramSuggestions(op *domain.Operation) []string {
	out := make([]string, 0, len(op.Params))
	for _, p := range op.Params {
		out = append(out, fmt.Sprintf("%s (%s)", p.Name, p.In))
	}
	return out
}

// checkExampleBody reports MISSING_BODY (error) when op requires a request
// body and ex has none, and UNEXPECTED_BODY (warning) when ex has a body
// but op takes none -- the same two checks and codes flow's checkBinding
// applies to a step. ex.Body may be a template (e.g. "${inputs.x}") or a
// plain value; only presence is checked here, exactly as PLAN §34b asks.
func checkExampleBody(ex domain.SavedExample, op *domain.Operation) []domain.Diagnostic {
	var diags []domain.Diagnostic
	hasBody := ex.Body != nil

	if op.RequestBody != nil && op.RequestBody.Required && !hasBody {
		diags = append(diags, domain.Diagnostic{
			Code: flow.CodeMissingBody, Severity: domain.SeverityError,
			Message: fmt.Sprintf("operation `%s` requires a request body", op.ID),
		})
	}
	if hasBody && op.RequestBody == nil {
		diags = append(diags, domain.Diagnostic{
			Code: flow.CodeUnexpectedBody, Severity: domain.SeverityWarning,
			Message: fmt.Sprintf("operation `%s` does not take a request body", op.ID),
		})
	}
	return diags
}

// hasErrorDiagnostic reports whether any diagnostic in diags has error
// severity (a warning alone, e.g. UNEXPECTED_BODY, does not block a
// write -- mirrors flow.Validator.Validate's Valid computation).
func hasErrorDiagnostic(diags []domain.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == domain.SeverityError {
			return true
		}
	}
	return false
}

// exampleInvalidErr builds the errs.Invalid error Create/Update return for
// an invalid example, carrying its diagnostics as a detail, shaped exactly
// like flow validation's Details["diagnostics"] (see flowInvalidErr in
// flows.go).
func exampleInvalidErr(diags []domain.Diagnostic) error {
	return errs.New(errs.Invalid, "example validation failed: %d problem(s)", len(diags)).
		WithDetail("diagnostics", diags)
}

// exampleSchemaDiagnostics renders ex as its file would be written
// (defaulting Version to 1, the same default example.Store.Create/Update
// apply, since ex's own Version is typically unset at this point -- the
// store, not the caller, is what actually decides it) and validates the
// result against the example file schema (spec.Example), converting any
// schema.Problem into a SCHEMA diagnostic. internal/example's own
// Parse/Marshal are not schema-checked (PLAN's file-format schema lives
// only here, at the API layer, ahead of the store's write), so a file
// dropped directly into an examples/ directory and picked up by Reindex is
// not schema-checked; only Create/Update are.
func exampleSchemaDiagnostics(ex domain.SavedExample) ([]domain.Diagnostic, error) {
	rendered := ex
	if rendered.Version == 0 {
		rendered.Version = 1
	}
	data, err := example.Marshal(&rendered)
	if err != nil {
		return nil, err
	}
	problems := spec.ValidateYAML(spec.Example, data)
	if len(problems) == 0 {
		return nil, nil
	}
	diags := make([]domain.Diagnostic, len(problems))
	for i, p := range problems {
		diags[i] = domain.Diagnostic{Code: flow.CodeSchema, Severity: domain.SeverityError, Message: p.String(), Line: p.Line}
	}
	return diags, nil
}

// exampleBodyTruncateLimit is the PLAN §34b limit past which an Expect
// string body is truncated, with a trailing marker, before being saved.
const exampleBodyTruncateLimit = 16 * 1024

// alwaysDroppedExampleHeaders are never carried into a saved example,
// regardless of value. Secrets and session state (Authorization,
// Proxy-Authorization, Cookie) must not travel in a portable file, and
// transport or tracing headers (User-Agent, Content-Type, Content-Length,
// request and trace ids, proxies' X-Forwarded-*) are set per request by the
// client that replays the example, so recording them would only pin noise
// from the original run.
var alwaysDroppedExampleHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"user-agent":          true,
	"content-type":        true,
	"content-length":      true,
	"transfer-encoding":   true,
	"accept":              true,
	"accept-encoding":     true,
	"accept-language":     true,
	"host":                true,
	"connection":          true,
	"keep-alive":          true,
	"date":                true,
	"expect":              true,
	"te":                  true,
	"upgrade":             true,
	"via":                 true,
	"x-request-id":        true,
	"x-correlation-id":    true,
	"traceparent":         true,
	"tracestate":          true,
	"x-b3-traceid":        true,
	"x-b3-spanid":         true,
	"x-b3-parentspanid":   true,
	"x-b3-sampled":        true,
	"x-amzn-trace-id":     true,
	"x-forwarded-for":     true,
	"x-forwarded-proto":   true,
	"x-forwarded-host":    true,
}

// liftHeaderParams moves every recorded header that matches one of op's
// declared header params (case-insensitively: the HTTP client canonicalises
// names, so "orgId" comes back as "Orgid") into input under the param's
// declared name, so the example replays standalone and reads as the
// contract does. Headers already covered by input are dropped; unrelated
// headers stay in headers.
func liftHeaderParams(op *domain.Operation, input map[string]any, headers map[string]string) (map[string]any, map[string]string) {
	if op == nil || len(headers) == 0 {
		return input, headers
	}
	for _, p := range op.Params {
		if p.In != domain.InHeader {
			continue
		}
		for k, v := range headers {
			if !strings.EqualFold(k, p.Name) {
				continue
			}
			if input == nil {
				input = map[string]any{}
			}
			if !hasKeyFold(input, p.Name) {
				input[p.Name] = v
			}
			delete(headers, k)
		}
	}
	if len(headers) == 0 {
		headers = nil
	}
	return input, headers
}

func hasKeyFold(m map[string]any, name string) bool {
	for k := range m {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// FromRun saves one step of a recorded run as a verified example: the
// request as sent (body, non-auth headers, path/query inputs recovered
// from the URL) and the observed response as Expect (PLAN §34b).
func (e *exampleAPI) FromRun(ctx context.Context, req engine.ExampleFromRun) (*domain.SavedExample, error) {
	l := e.l

	run, err := l.Runs().Get(ctx, req.RunID)
	if err != nil {
		return nil, err
	}
	step, err := pickRunStep(run, req.StepID)
	if err != nil {
		return nil, err
	}
	if step.Operation == "" {
		return nil, errs.New(errs.Invalid, "run %q step %q has no recorded operation", run.ID, step.StepID).
			WithDetail("run_id", run.ID).WithDetail("step_id", step.StepID)
	}
	if step.Request == nil {
		return nil, errs.New(errs.Invalid, "run %q step %q has no recorded request", run.ID, step.StepID).
			WithDetail("run_id", run.ID).WithDetail("step_id", step.StepID)
	}

	ex := domain.SavedExample{
		ID:          req.ID,
		Operation:   step.Operation,
		Description: req.Description,
		Scope:       req.Scope,
		Tags:        req.Tags,
		Body:        parseJSONOrRaw(step.Request.Body, step.Request.BodyRaw),
		Input:       exampleInputFromURL(ctx, l, step.Operation, step.Request.URL),
		Headers:     filterExampleHeaders(step.Request.Headers),
	}
	if op, opErr := l.Catalog().GetOperation(ctx, step.Operation); opErr == nil && op != nil {
		ex.Input, ex.Headers = liftHeaderParams(op, ex.Input, ex.Headers)
	}

	if step.Response != nil {
		ex.Expect = &domain.ExampleExpect{
			Status: step.Response.Status,
			Body:   truncateExampleBody(parseJSONOrRaw(step.Response.Body, step.Response.BodyRaw)),
		}
	}

	source := req.Source
	if source == nil {
		source = &domain.MemorySource{Kind: "user"}
	}
	ex.Verified = &domain.ExampleVerified{
		Env: run.Environment, RunID: run.ID, StepID: step.StepID, At: time.Now().UTC(), Source: source,
	}

	return l.exStore.Create(ctx, ex)
}

// pickRunStep selects the step FromRun should save: the step named stepID,
// or -- when stepID is empty -- the run's only step, or (a multi-step run)
// its first step that has a recorded request.
func pickRunStep(run *domain.Run, stepID string) (*domain.StepResult, error) {
	if stepID != "" {
		for i := range run.Steps {
			if run.Steps[i].StepID == stepID {
				return &run.Steps[i], nil
			}
		}
		return nil, errs.New(errs.Invalid, "run %q has no step %q", run.ID, stepID).
			WithDetail("run_id", run.ID).WithDetail("step_id", stepID)
	}
	if len(run.Steps) == 1 {
		return &run.Steps[0], nil
	}
	for i := range run.Steps {
		if run.Steps[i].Request != nil {
			return &run.Steps[i], nil
		}
	}
	return nil, errs.New(errs.Invalid, "run %q has no step with a recorded request", run.ID).
		WithDetail("run_id", run.ID)
}

// parseJSONOrRaw returns body if it is non-nil (already-parsed JSON), else
// raw parsed as JSON when it looks like JSON, else raw itself as a plain
// string. Returns nil when both are empty.
func parseJSONOrRaw(body any, raw string) any {
	if body != nil {
		return body
	}
	if raw == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
		return parsed
	}
	return raw
}

// truncateExampleBody truncates body when it is a string longer than
// exampleBodyTruncateLimit, appending a trailing marker (PLAN §34b). A
// non-string body (already-parsed JSON) is returned unchanged.
func truncateExampleBody(body any) any {
	s, ok := body.(string)
	if !ok || len(s) <= exampleBodyTruncateLimit {
		return body
	}
	return s[:exampleBodyTruncateLimit] + "...[truncated]"
}

// filterExampleHeaders copies headers, dropping alwaysDroppedExampleHeaders
// (Authorization, Proxy-Authorization, Cookie) and any header whose value
// already looks redacted ("[REDACTED]", or containing "***") -- runtime's
// own redactor (internal/runtime/redact.go) may have already scrubbed a
// secret's value to one of those before the run was persisted. Returns nil
// rather than an empty map when nothing is left, so an example with no
// headers omits the key entirely (domain.SavedExample.Headers is
// omitempty).
func filterExampleHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if alwaysDroppedExampleHeaders[strings.ToLower(k)] {
			continue
		}
		if looksRedacted(v) {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// looksRedacted reports whether v is a placeholder a redactor left behind
// rather than a real header value.
func looksRedacted(v string) bool {
	t := strings.TrimSpace(v)
	if strings.EqualFold(t, "[redacted]") {
		return true
	}
	return strings.Contains(t, "***")
}

// exampleInputFromURL recovers Input from rawURL: path parameters bound by
// matching rawURL's path against opID's HTTP path template segment by
// segment ({name} binds), plus every query parameter (a repeated key
// becomes a []string; a single value stays a plain string). Returns nil
// when rawURL doesn't parse or nothing was recovered.
func exampleInputFromURL(ctx context.Context, l *Local, opID, rawURL string) map[string]any {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}

	input := map[string]any{}
	if op, err := l.cat.GetOperation(ctx, opID); err == nil && op.HTTP != nil {
		for name, value := range pathParamsFromURL(op.HTTP.Path, u.Path) {
			input[name] = value
		}
	}
	for k, v := range u.Query() {
		if len(v) == 1 {
			input[k] = v[0]
		} else {
			input[k] = v
		}
	}
	if len(input) == 0 {
		return nil
	}
	return input
}

// pathParamsFromURL matches template ("/v1/riders/{riderId}") against
// actual ("/v1/riders/R123") segment by segment, binding each "{name}"
// template segment to the corresponding actual segment (URL-unescaped).
// The two are anchored at their last segment (rather than their first) so
// a base path the operation's template doesn't include (a mock server
// prefix, a gateway route) doesn't shift the alignment; when actual has
// fewer segments than template, nothing can be recovered.
func pathParamsFromURL(template, actual string) map[string]string {
	tmplSegs := splitURLPath(template)
	actualSegs := splitURLPath(actual)
	out := map[string]string{}
	if len(actualSegs) < len(tmplSegs) {
		return out
	}
	offset := len(actualSegs) - len(tmplSegs)
	for i, seg := range tmplSegs {
		if !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
		value := actualSegs[i+offset]
		if unescaped, err := url.PathUnescape(value); err == nil {
			value = unescaped
		}
		out[name] = value
	}
	return out
}

// splitURLPath splits a URL path into its non-empty segments.
func splitURLPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// fillExampleShipped fills Shipped on every workspace-tier example in exs,
// in place, from one read-only look at the workspace's git repository
// (PLAN §7b) -- mirrors flowAPI's fillShipped for flows and
// memoryAPI.fillMemoryShipped for memories. Service-tier examples are left
// alone. Any git failure leaves every Shipped empty and is logged at
// Debug: a listing must never fail because git did. Only runs FileStates
// at all when at least one result is workspace tier.
func (l *Local) fillExampleShipped(ctx context.Context, exs []domain.SavedExample) {
	var idx []int
	var paths []string
	for i := range exs {
		if exs[i].Tier != domain.TierWorkspace {
			continue
		}
		idx = append(idx, i)
		paths = append(paths, exs[i].Path)
	}
	if len(idx) == 0 {
		return
	}
	if _, ok := l.gitMgr.RepoRoot(ctx, l.ws.Dir); !ok {
		return
	}
	states, err := l.gitMgr.FileStates(ctx, l.ws.Dir, paths)
	if err != nil {
		l.logger.Debug("computing example ship states failed; leaving them empty", "error", err)
		return
	}
	for _, i := range idx {
		exs[i].Shipped = states[exs[i].Path]
	}
}

// Move places a workspace-scope example's file in another tier (PLAN §7b),
// mirroring memoryAPI.Move: service scope is refused (its single home is
// the service's own repo), tier must be domain.TierLocal or
// domain.TierWorkspace, and the actual move rides Update (which already
// knows how to rewrite an example's file at a new PathFor and remove the
// old one). Moving to the tier an example is already in is a no-op that
// still returns the current item (with Shipped).
func (e *exampleAPI) Move(ctx context.Context, id, tier string) (*domain.SavedExample, error) {
	existing, err := e.l.exStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing.Scope != domain.ExampleScopeWorkspace {
		return nil, errs.New(errs.Invalid, "only a workspace-scope example can move between tiers; %q is %s scope", id, existing.Scope).
			WithHint("a service-scoped example has a single home in the service's own repo")
	}
	if tier != domain.TierLocal && tier != domain.TierWorkspace {
		return nil, errs.New(errs.Invalid, "unknown tier %q for Move; want %s or %s", tier, domain.TierLocal, domain.TierWorkspace)
	}
	if existing.Tier == tier {
		return e.Get(ctx, id)
	}

	toMove := *existing
	toMove.Tier = tier
	if _, err := e.Update(ctx, toMove); err != nil {
		return nil, err
	}
	return e.Get(ctx, id)
}

// Commit records a workspace-tier example's file in the workspace
// repository with one commit of that file (PLAN §7b), mirroring
// memoryAPI.Commit and flowAPI.Commit -- see their doc comments for the
// full rationale, reused here via shipStateNothingToCommit. Refused with
// errs.Invalid for the service tier, a workspace not inside a git
// repository, and a file with nothing to commit.
//
// Note: unlike memory.changed/flow.changed, there is currently no
// example.changed engine event, so -- like exampleAPI.Update today --
// Commit does not emit one; a caller that wants to learn about the commit
// reads the returned example (its Shipped is ShipUnpushed).
func (e *exampleAPI) Commit(ctx context.Context, id, message string) (*domain.SavedExample, error) {
	l := e.l
	existing, err := l.exStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing.Tier != domain.TierWorkspace {
		tier := existing.Tier
		if tier == "" {
			tier = string(existing.Scope)
		}
		return nil, errs.New(errs.Invalid, "only a workspace-tier example can be committed; %q is %s", id, tier).
			WithHint("move it to the workspace tier first (`sapien example move " + id + " workspace`)")
	}
	if _, ok := l.gitMgr.RepoRoot(ctx, l.ws.Dir); !ok {
		return nil, errs.New(errs.Invalid, "workspace %s is not in a git repository", l.ws.Dir)
	}

	states, err := l.gitMgr.FileStates(ctx, l.ws.Dir, []string{existing.Path})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "checking the git state of %s", existing.Path)
	}
	state := states[existing.Path]
	if sentence := shipStateNothingToCommit(state); sentence != "" {
		return nil, errs.New(errs.Invalid, "example %q has nothing to commit: %s", id, sentence)
	}

	if message == "" {
		if state == domain.ShipUntracked {
			message = fmt.Sprintf("Add example %s to the team workspace", id)
		} else {
			message = fmt.Sprintf("Update example %s", id)
		}
	}
	if _, err := l.gitMgr.CommitPaths(ctx, l.ws.Dir, []string{existing.Path}, message); err != nil {
		return nil, err
	}

	updated, err := l.exStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	updated.Shipped = domain.ShipUnpushed
	return updated, nil
}
