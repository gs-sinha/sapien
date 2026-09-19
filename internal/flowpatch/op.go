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
	// step id, as a sibling within the same list -- the top-level Phase
	// list, or, when Into is set, that block's own nested `steps:` list;
	// at most one of After/Before may be set. Neither set appends to the
	// end of the list.
	After  string `json:"after,omitempty"`
	Before string `json:"before,omitempty"`
	// Phase selects add_step's target list: "" (default) for the main
	// `steps:`, "setup", or "teardown". A phase list that doesn't exist yet
	// is created. Ignored when Into is set (a block's nested steps live
	// wherever the block itself does, not in a phase list of their own).
	Phase string `json:"phase,omitempty"`
	// Into is a loop block's step id (PLAN §34f.8): when set, the new step
	// is added inside that block's own `steps:` list (After/Before then
	// name siblings inside that block) instead of a top-level phase list.
	// Mutually exclusive with Phase in effect, though not in validation --
	// Phase is simply ignored when Into is set.
	Into string `json:"into,omitempty"`

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
// can still express via Step). `steps` (a block's nested step list) is
// deliberately NOT included (PLAN §34f.8): a block's nested steps are
// edited individually by id, or the whole block is replaced with set_step.
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
	"when":    true,
	// Loop block fields (PLAN §34f.8).
	"foreach":    true,
	"repeat":     true,
	"max":        true,
	"break_when": true,
	"on_error":   true,
}
