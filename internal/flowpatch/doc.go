// Package flowpatch applies small, targeted edits to a *.flow.yaml
// document's own YAML node tree (gopkg.in/yaml.v3's Node API) instead of
// requiring a caller to resend the whole document. Operating on the node
// tree, rather than round-tripping through domain.Flow and re-marshaling,
// is what lets comments, key order, and the formatting of every part of the
// document an operation doesn't touch survive as well as yaml.v3 allows.
//
// This exists because editing one assertion in an 800-line flow by
// resending the entire YAML through update_flow is expensive and easy to
// get wrong (docs/feedback/2026-09-05-41-step-flow-session.md, item 3):
// "Authoring is all-or-nothing... Accept a file path, or a step-level
// patch." Apply is the step-level patch; internal/mcp's patch_flow tool and
// `sapien flow patch` are its callers.
//
// # What is preserved
//
// For any step or top-level key Apply doesn't touch: its key order, its
// comments (HeadComment/LineComment/FootComment), and its scalar/flow
// style (quoted vs. plain, `{ }`/`[ ]` inline vs. block) -- because Apply
// never re-encodes a node it isn't changing, it only edits the specific
// yaml.Node(s) an op names. The document's own indent width (2 or 4 spaces
// per nesting level, auto-detected -- see detectIndentWidth) is applied to
// the whole re-serialized file, touched or not, since yaml.v3 has one
// indent setting per document, not per node; yaml.v3's own default is 4,
// which would otherwise silently reflow a 2-space file even though nothing
// in it changed.
//
// merge_step changes or adds only the fields it's given, in place, on the
// step's existing node -- so the step's own leading comment survives (a
// Notes entry says so, since the fields under it changed) and a NEW field
// is inserted at its conventional position (see conventionalStepKeyOrder),
// not appended after whatever was merged first. set_step and remove_step
// take the replaced/removed step's own comment out of the document with it
// (also a Notes entry), rather than leaving it standing above whatever
// step now occupies that position. add_step writes a brand-new step's keys
// in conventional order, never yaml.v3's default (alphabetical, since a
// step decoded from JSON is a map[string]any, which has no order of its
// own).
//
// # What is not preserved
//
// Blank lines: yaml.v3's node tree has no representation for a blank line
// between two mapping or sequence entries, so Apply cannot reproduce them
// -- a hand-formatted flow's blank lines between steps collapse on any
// Apply call, even one that touches a different, unrelated step.
//
// The scalar/flow style of content Apply itself generates: a brand-new
// step (add_step), a field merge_step sets or adds, or a value set_step
// writes always renders in yaml.v3's default style (block mappings/lists,
// plain scalars unless quoting is required) -- never copying a
// neighboring step's inline `{ a: 1 }`/`[1, 2]` style, even if the rest of
// the document uses it throughout. A comment attached to a value
// merge_step or set_step replaces (not the step's own leading comment, but
// e.g. one inside the old `body:` a merge_step's `body` field replaced) is
// dropped along with that value; only the step's own leading comment is
// tracked in Notes.
package flowpatch
