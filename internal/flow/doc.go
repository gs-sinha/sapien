// Package flow parses and validates Sapien flow files (*.flow.yaml, PLAN.md
// §8): turning YAML into a domain.Flow with source line numbers attached to
// every step and assertion, validating the result against an API catalog
// with diagnostics an external agent can act on (PLAN.md §23.1), and a
// handful of small file helpers (Save, DefaultPath) used by the engine and
// the MCP layer.
//
// Parsing (Parse, ParseFile) only checks that the YAML is well-formed and
// matches the flow JSON Schema (internal/spec); it never needs a catalog.
// Validation (Validator.Validate, Validator.ValidateSource) additionally
// checks a parsed flow against a Catalog: operation existence, parameter
// binding, and every `${...}`/assert/extract/until expression's static
// references (steps, inputs, response fields).
package flow
