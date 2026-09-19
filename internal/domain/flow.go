package domain

import "gopkg.in/yaml.v3"

import "time"

// Flow is the declarative, executable composition of operations (PLAN §8).
type Flow struct {
	Version     int                  `yaml:"version" json:"version"`
	ID          string               `yaml:"id,omitempty" json:"id"` // default: file name stem
	Name        string               `yaml:"name,omitempty" json:"name,omitempty"`
	Description string               `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string             `yaml:"tags,omitempty" json:"tags,omitempty"`
	Inputs      map[string]InputSpec `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	// Setup runs before Steps and its results are addressable as steps.<id>
	// like any other step; a resumed run reuses setup results by default.
	Setup []Step `yaml:"setup,omitempty" json:"setup,omitempty"`
	Steps []Step `yaml:"steps" json:"steps"`
	// Teardown always runs after Steps, even when a step failed or errored,
	// so a flow can release what it created; its failures never change the
	// run's outcome, they are reported on the teardown steps themselves.
	Teardown []Step `yaml:"teardown,omitempty" json:"teardown,omitempty"`

	// Populated by the loader, not part of the file.
	Path      string `yaml:"-" json:"path,omitempty"`
	OwnerKind string `yaml:"-" json:"owner_kind,omitempty"` // FlowOwnerLocal | FlowOwnerWorkspace | FlowOwnerService
	OwnerID   string `yaml:"-" json:"owner_id,omitempty"`
	Source    string `yaml:"-" json:"source,omitempty"` // raw YAML; carried over the daemon API so Remote, the MCP bridge, and the UI see it
	// Folder is the subfolder of the owning tier's flows directory Path sits
	// in (PLAN §34f item 6): "" at the root, "/"-separated otherwise. Derived
	// from Path, never written to the file; not omitempty so "" (root)
	// always shows up on the wire rather than being silently absent.
	Folder string `yaml:"-" json:"folder"`
}

// InputSpec declares a flow input.
type InputSpec struct {
	Type        string `yaml:"type,omitempty" json:"type,omitempty"` // string|integer|number|boolean|object|array
	Required    bool   `yaml:"required,omitempty" json:"required,omitempty"`
	Default     any    `yaml:"default,omitempty" json:"default,omitempty"` // may contain ${...}
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// Step is one operation call inside a flow.
type Step struct {
	ID   string `yaml:"id" json:"id"`
	Call string `yaml:"call" json:"call"` // operation ID
	// Example names a saved example (PLAN §34b) to reuse: resolution fills
	// Call, Input, Body, and Headers from it, with any of those set
	// directly on the step taking precedence. A step must set Call,
	// Example, or both (when both, Call must match the example's
	// operation). See internal/flow.Materialize.
	Example string `yaml:"example,omitempty" json:"example,omitempty"`
	// When is a CEL boolean (inputs/env/steps only, no `status`/`body`/...:
	// this step hasn't run yet) evaluated before the request is built; false
	// records the step `skipped` (SkipReason "when") without sending a
	// request or evaluating assertions, and the run continues (PLAN §34f.7).
	When    string            `yaml:"when,omitempty" json:"when,omitempty"`
	Input   map[string]any    `yaml:"input,omitempty" json:"input,omitempty"` // bound by name to path/query/header params
	Params  *ExplicitParams   `yaml:"params,omitempty" json:"params,omitempty"`
	Body    any               `yaml:"body,omitempty" json:"body,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Extract map[string]string `yaml:"extract,omitempty" json:"extract,omitempty"` // name -> CEL over the response
	Assert  []Assertion       `yaml:"assert,omitempty" json:"assert,omitempty"`
	Until   string            `yaml:"until,omitempty" json:"until,omitempty"` // CEL; step is polled until true
	Poll    *Poll             `yaml:"poll,omitempty" json:"poll,omitempty"`
	Timeout string            `yaml:"timeout,omitempty" json:"timeout,omitempty"` // per-request timeout, e.g. "10s"

	// Block fields (PLAN §34f.8): a step with Steps set and no Call/Example
	// is a loop block -- every call-only field above must be empty on it
	// (BLOCK_SHAPE) -- that runs its nested Steps repeatedly: once per
	// element of Foreach (a CEL expression over a list), or per Repeat's
	// rules. Exactly one of Foreach/Repeat is set. Blocks cannot nest
	// (NESTED_LOOP) and are not allowed in Setup/Teardown (LOOP_IN_PHASE).
	Foreach string  `yaml:"foreach,omitempty" json:"foreach,omitempty"` // CEL -> list; iter.item is each element (PLAN §34f.8 calls this loop.item; see internal/expr.IterValue for why it is `iter` here)
	Repeat  *Repeat `yaml:"repeat,omitempty" json:"repeat,omitempty"`
	// Max caps Foreach's iteration count (default 100 when unset, hard
	// limit 1000): a list longer than Max fails the block before iterating
	// -- it never silently truncates. Unused for a Repeat block, which
	// takes its own required Repeat.Max instead.
	Max int `yaml:"max,omitempty" json:"max,omitempty"`
	// BreakWhen is a CEL boolean evaluated after each iteration (with
	// access to that iteration's nested steps and iter.item/iter.index);
	// true ends the loop after that iteration, same as running out of
	// list/reaching Repeat's own stop condition.
	BreakWhen string `yaml:"break_when,omitempty" json:"break_when,omitempty"`
	// OnError is "stop" (default) or "continue": whether a failed nested
	// step ends the whole block (and the run, as today) or lets the loop
	// keep iterating, with the block's own final status still failed if any
	// iteration failed.
	OnError string `yaml:"on_error,omitempty" json:"on_error,omitempty"`
	// Steps is a block's nested step list; a non-block step must leave it
	// nil (BLOCK_SHAPE).
	Steps []Step `yaml:"steps,omitempty" json:"steps,omitempty"`

	Line int `yaml:"-" json:"line,omitempty"`
}

// Repeat configures a repeat block (PLAN §34f.8): re-run Steps until Until
// is true (checked after each iteration), or while While stays true
// (checked before each iteration), up to Max iterations, waiting Interval
// between them. At least one of Until/While is required, and Max is always
// required (1..1000) -- unlike Foreach's Max, a repeat has no natural
// default.
type Repeat struct {
	Until    string `yaml:"until,omitempty" json:"until,omitempty"`
	While    string `yaml:"while,omitempty" json:"while,omitempty"`
	Max      int    `yaml:"max" json:"max"`
	Interval string `yaml:"interval,omitempty" json:"interval,omitempty"` // duration, e.g. "1s"; default: no wait
}

// IsBlock reports whether s is a loop block (Steps set) rather than a call
// step. A well-formed flow (BLOCK_SHAPE checked) never sets both Steps and
// Call/Example on the same step.
func (s Step) IsBlock() bool { return len(s.Steps) > 0 }

// ExplicitParams is the disambiguated form of Step.Input.
type ExplicitParams struct {
	Path    map[string]any    `yaml:"path,omitempty" json:"path,omitempty"`
	Query   map[string]any    `yaml:"query,omitempty" json:"query,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
}

// Poll controls `until` polling.
type Poll struct {
	Interval string `yaml:"interval,omitempty" json:"interval,omitempty"` // default "1s"
	Timeout  string `yaml:"timeout,omitempty" json:"timeout,omitempty"`   // default "30s"
}

// Assertion is either a bare CEL expression (Expr) or one structured form.
// Structured forms compile to CEL (or the schema validator) so there is one execution path.
type Assertion struct {
	Expr      string `yaml:"expr,omitempty" json:"expr,omitempty"` // a bare string assertion, or `expr:` in the object form; both are accepted on input
	Status    *int   `yaml:"status,omitempty" json:"status,omitempty"`
	LatencyMs *Range `yaml:"latency_ms,omitempty" json:"latency_ms,omitempty"`
	Schema    string `yaml:"schema,omitempty" json:"schema,omitempty"` // "contract"
	Path      string `yaml:"path,omitempty" json:"path,omitempty"`     // e.g. body.riderId
	Eq        any    `yaml:"eq,omitempty" json:"eq,omitempty"`
	Neq       any    `yaml:"neq,omitempty" json:"neq,omitempty"`
	Exists    *bool  `yaml:"exists,omitempty" json:"exists,omitempty"`
	Matches   string `yaml:"matches,omitempty" json:"matches,omitempty"` // regex
	Contains  any    `yaml:"contains,omitempty" json:"contains,omitempty"`
	// Lt/Lte/Gt/Gte compare Path's value with an ordering operator instead
	// of equality (CEL's own numeric/string ordering); like Eq/Neq/Contains,
	// each accepts a `${...}` template and is interpolated the same way,
	// keeping the interpolated value's native type.
	Lt      any    `yaml:"lt,omitempty" json:"lt,omitempty"`
	Lte     any    `yaml:"lte,omitempty" json:"lte,omitempty"`
	Gt      any    `yaml:"gt,omitempty" json:"gt,omitempty"`
	Gte     any    `yaml:"gte,omitempty" json:"gte,omitempty"`
	Message string `yaml:"message,omitempty" json:"message,omitempty"`
	// Soft records a mismatch as a warning on the step instead of failing
	// it: the run stays green, the result is kept, and a later run reports
	// when the assertion starts (or stops) passing.
	Soft bool `yaml:"soft,omitempty" json:"soft,omitempty"`
	Line int  `yaml:"-" json:"line,omitempty"`
}

// Range is a numeric comparison used by latency assertions.
type Range struct {
	Lt  *float64 `yaml:"lt,omitempty" json:"lt,omitempty"`
	Lte *float64 `yaml:"lte,omitempty" json:"lte,omitempty"`
	Gt  *float64 `yaml:"gt,omitempty" json:"gt,omitempty"`
	Gte *float64 `yaml:"gte,omitempty" json:"gte,omitempty"`
}

// FlowSummary is the catalog view of a flow.
type FlowSummary struct {
	ID         string    `json:"id"`
	Name       string    `json:"name,omitempty"`
	Path       string    `json:"path"`
	OwnerKind  string    `json:"owner_kind"`
	OwnerID    string    `json:"owner_id,omitempty"`
	Tags       []string  `json:"tags,omitempty"`
	Operations []string  `json:"operations,omitempty"`
	StepCount  int       `json:"step_count"`
	Hash       string    `json:"hash"`
	Updated    time.Time `json:"updated"`
	// Shipped says how far a workspace-tier flow's file has travelled
	// towards the team, from a read-only look at the workspace repository:
	// ShipUntracked, ShipModified, ShipUnpushed or ShipShipped. Empty for
	// the local and service tiers, and when the workspace is not in git.
	Shipped string `json:"shipped,omitempty"`
	// Folder is the subfolder of the owning tier's flows directory the flow
	// sits in (PLAN §34f item 6): "" at the root, "/"-separated otherwise.
	// Derived from Path; not omitempty so "" (root) always shows on the wire.
	Folder string `json:"folder"`
}

// Ship states for FlowSummary.Shipped (PLAN §7b). Promotion moves a file
// into the team's directory; these say whether a human has committed and
// pushed it since, because a moved file nobody commits is the silent
// failure the tiers exist to remove.
const (
	ShipUntracked = "untracked" // in flows/ but never added to git
	ShipModified  = "modified"  // tracked, with uncommitted changes
	ShipUnpushed  = "unpushed"  // committed on a branch the remote does not have yet, or no upstream
	ShipShipped   = "shipped"   // committed and on the upstream
)

// Severity of a diagnostic.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is one validation finding with enough context for an agent to repair it.
type Diagnostic struct {
	Code        string   `json:"code"`
	Severity    Severity `json:"severity"`
	Message     string   `json:"message"`
	Line        int      `json:"line,omitempty"`
	Column      int      `json:"column,omitempty"`
	StepID      string   `json:"step_id,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`
}

// ValidationResult is the outcome of validating a flow.
type ValidationResult struct {
	Valid       bool         `json:"valid"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// UnmarshalYAML accepts both assertion spellings the DSL allows: a bare
// CEL string (`- status == 200`) and the mapping form (`- expr: status ==
// 200`, or the structured keys). Every reader that decodes a flow through
// the domain types, the engine's fake included, therefore round-trips what
// get_flow prints.
func (a *Assertion) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		a.Expr = value.Value
		a.Line = value.Line
		return nil
	}
	type plain Assertion
	var p plain
	if err := value.Decode(&p); err != nil {
		return err
	}
	*a = Assertion(p)
	a.Line = value.Line
	return nil
}
