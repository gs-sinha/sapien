package flowpatch

// Op kinds accepted by Apply.
const (
	// KindSetStep replaces the whole step mapping named by ID with Step, in
	// whichever of setup/steps/teardown currently holds it.
	KindSetStep = "set_step"
	// KindMergeStep sets or replaces the given top-level keys of the step
	// named by ID (Fields), leaving every other key as it was.
	KindMergeStep = "merge_step"
	// KindAddStep inserts Step (which must carry its own `id`) into the
	// steps list named by Phase ("" for the main `steps:`, "setup", or
	// "teardown"), positioned via After or Before (mutually exclusive), or
	// appended at the end when neither is set.
	KindAddStep = "add_step"
	// KindRemoveStep deletes the step named by ID from whichever of
	// setup/steps/teardown currently holds it.
	KindRemoveStep = "remove_step"
	// KindSetInputs replaces the flow's top-level `inputs:` mapping with
	// Inputs entirely.
	KindSetInputs = "set_inputs"
	// KindSetMeta sets any of the flow's Name, Description, and Tags (via
	// Meta); a nil field is left unchanged.
	KindSetMeta = "set_meta"
)

// Op is one edit for Apply. Only the fields relevant to Kind need be set;
// Apply ignores the others. Op decodes directly from JSON (e.g. an MCP
// patch_flow call's `ops` array, or a CLI `--ops @file.json`).
type Op struct {
	// Kind selects the operation: one of the Kind* constants above.
	Kind string `json:"kind"`

	// ID names the target step for set_step, merge_step, and remove_step.
	ID string `json:"id,omitempty"`
	// Step is the replacement step (set_step) or new step (add_step),
	// decoded as a YAML/JSON mapping (map[string]any once decoded from
	// JSON). add_step requires it to carry its own `id`; set_step's id, if
	// present, must match ID.
	Step any `json:"step,omitempty"`
	// Fields is merge_step's set of top-level step keys to set or replace:
	// input, body, headers, assert, extract, until, poll, timeout, call,
	// example. Any other key is rejected.
	Fields map[string]any `json:"fields,omitempty"`

	// After/Before position add_step's new step relative to an existing
	// step id in the same Phase list; at most one may be set. Neither set
	// appends to the end of the list.
	After  string `json:"after,omitempty"`
	Before string `json:"before,omitempty"`
	// Phase selects add_step's target list: "" (default) for the main
	// `steps:`, "setup", or "teardown". A phase list that doesn't exist yet
	// is created.
	Phase string `json:"phase,omitempty"`

	// Inputs is set_inputs' replacement for the flow's `inputs:` mapping.
	Inputs map[string]any `json:"inputs,omitempty"`
	// Meta is set_meta's fields; see Meta.
	Meta *Meta `json:"meta,omitempty"`
}

// Meta is set_meta's argument: each non-nil field replaces that flow-level
// key; a nil field leaves the existing value (or absence) untouched. Tags
// is a pointer-to-slice so an explicit empty list (clear the tags) is
// distinguishable from "don't touch tags".
type Meta struct {
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
}

// allowedMergeFields is merge_step's whitelist of step keys (mirrors
// internal/domain.Step, minus `id`, which identifies the step rather than
// describing it, and `params`, the explicit-params form set_step/add_step
// can still express via Step).
var allowedMergeFields = map[string]bool{
	"input":   true,
	"body":    true,
	"headers": true,
	"assert":  true,
	"extract": true,
	"until":   true,
	"poll":    true,
	"timeout": true,
	"call":    true,
	"example": true,
}
