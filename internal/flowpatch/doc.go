// Package flowpatch applies small, targeted edits to a *.flow.yaml
// document's own YAML node tree (gopkg.in/yaml.v3's Node API) instead of
// requiring a caller to resend the whole document. Operating on the node
// tree, rather than round-tripping through domain.Flow and re-marshaling,
// is what lets comments, key order, and the formatting of every part of the
// document an operation doesn't touch survive untouched.
//
// This exists because editing one assertion in an 800-line flow by
// resending the entire YAML through update_flow is expensive and easy to
// get wrong (docs/feedback/2026-09-05-41-step-flow-session.md, item 3):
// "Authoring is all-or-nothing... Accept a file path, or a step-level
// patch." Apply is the step-level patch; internal/mcp's patch_flow tool and
// `sapien flow patch` are its callers.
package flowpatch
