package spec

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// Problem is one validation finding, in a form cheap for a CLI or an MCP
// tool response to render.
//
// Path is a dotted/bracketed rendering of the JSON Schema instance location,
// e.g. "steps[1].call" or "" for the document root. Message is a short,
// human description with no location prefix (Path/Line already carry that).
// Line is the best-effort 1-based source line ValidateYAML could map the
// problem back to; it is 0 when unknown, and always 0 from Validate (which
// has no source text to consult).
type Problem struct {
	Path    string
	Message string
	Line    int
}

// String renders "path: message", or just "message" when Path is the
// document root.
func (p Problem) String() string {
	if p.Path == "" {
		return p.Message
	}
	return p.Path + ": " + p.Message
}

// rawProblem is a Problem before Path has been rendered from tokens and
// before ValidateYAML has had a chance to look up a Line.
type rawProblem struct {
	tokens  []string
	message string
}

var (
	compileOnce sync.Once
	compiled    map[Kind]*jsonschema.Schema
	compileErr  error
)

func compileAll() {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()

	compiled = make(map[Kind]*jsonschema.Schema, len(allKinds))
	for _, k := range allKinds {
		name := k.fileName()
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(Schema(k)))
		if err != nil {
			compileErr = fmt.Errorf("spec: parse %s: %w", name, err)
			return
		}
		if err := c.AddResource(name, doc); err != nil {
			compileErr = fmt.Errorf("spec: add resource %s: %w", name, err)
			return
		}
	}
	for _, k := range allKinds {
		name := k.fileName()
		sch, err := c.Compile(name)
		if err != nil {
			compileErr = fmt.Errorf("spec: compile %s: %w", name, err)
			return
		}
		compiled[k] = sch
	}
}

func schemaFor(k Kind) (*jsonschema.Schema, error) {
	compileOnce.Do(compileAll)
	if compileErr != nil {
		return nil, compileErr
	}
	sch, ok := compiled[k]
	if !ok {
		return nil, fmt.Errorf("spec: unknown kind %q", k)
	}
	return sch, nil
}

// validateRaw runs the compiled schema for kind against doc (which must
// already be built from JSON-schema-friendly types: map[string]any, []any,
// string, bool, nil, and Go's native numeric types) and flattens the
// resulting error tree into leaf-level problems.
func validateRaw(kind Kind, doc any) ([]rawProblem, error) {
	sch, err := schemaFor(kind)
	if err != nil {
		return nil, err
	}
	verr := sch.Validate(doc)
	if verr == nil {
		return nil, nil
	}
	ve, ok := verr.(*jsonschema.ValidationError)
	if !ok {
		// Should not happen: Schema.Validate only ever returns
		// *jsonschema.ValidationError or nil. Surface it anyway rather than
		// silently dropping information.
		return []rawProblem{{message: verr.Error()}}, nil
	}
	var out []rawProblem
	collectLeaves(ve, &out)
	sort.SliceStable(out, func(i, j int) bool {
		return pathString(out[i].tokens) < pathString(out[j].tokens)
	})
	return out, nil
}

// collectLeaves walks a jsonschema.ValidationError tree and appends one
// rawProblem per leaf (a node with no Causes). Container kinds -- Group
// (multiple sibling failures), AllOf/AnyOf/OneOf, and $ref/Reference
// indirection -- always carry at least one Cause for our schemas, so
// recursing into Causes and skipping the container's own (generic) message
// yields the concrete, actionable failures instead.
func collectLeaves(ve *jsonschema.ValidationError, out *[]rawProblem) {
	if len(ve.Causes) == 0 {
		*out = append(*out, rawProblem{
			tokens:  ve.InstanceLocation,
			message: describeKind(ve.ErrorKind),
		})
		return
	}
	for _, cause := range ve.Causes {
		collectLeaves(cause, out)
	}
}

// pathString renders instance-location tokens as "steps[1].call": array
// indices (all-digit tokens) attach with brackets and no separator, object
// keys are dot-joined. A pure-digit map key would render identically to an
// array index; none of Sapien's five schemas key a map by digit strings, so
// this ambiguity does not arise in practice.
func pathString(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, t := range tokens {
		if isIndexToken(t) {
			sb.WriteByte('[')
			sb.WriteString(t)
			sb.WriteByte(']')
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('.')
		}
		sb.WriteString(t)
	}
	return sb.String()
}

func isIndexToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Validate validates doc (already a plain Go value tree: map[string]any,
// []any, string, bool, nil, or a native numeric type -- exactly what
// encoding/json or a normalized YAML decode produces) against kind's schema.
// It returns nil when doc is valid. Every returned Problem has Line == 0;
// use ValidateYAML to get source line numbers.
func Validate(kind Kind, doc any) []Problem {
	raw, err := validateRaw(kind, doc)
	if err != nil {
		return []Problem{{Message: err.Error()}}
	}
	if len(raw) == 0 {
		return nil
	}
	out := make([]Problem, len(raw))
	for i, r := range raw {
		out[i] = Problem{Path: pathString(r.tokens), Message: r.message}
	}
	return out
}

// ValidateYAML parses src as YAML, validates it against kind's schema, and
// (best effort) annotates each Problem with the source line it came from.
//
// src is parsed twice: once into a generic `any` tree (normalized to fix up
// yaml.v3's timestamp auto-detection, see normalizeYAML) for validation
// against the compiled schema, and once into a yaml.Node tree purely to
// recover line numbers for the reported instance locations. A YAML parse
// error itself is reported as a single Problem with Line 0.
func ValidateYAML(kind Kind, src []byte) []Problem {
	var generic any
	if err := yaml.Unmarshal(src, &generic); err != nil {
		return []Problem{{Message: "invalid YAML: " + err.Error()}}
	}
	doc := normalizeYAML(generic)

	raw, err := validateRaw(kind, doc)
	if err != nil {
		return []Problem{{Message: err.Error()}}
	}
	if len(raw) == 0 {
		return nil
	}

	var root yaml.Node
	haveNodes := yaml.Unmarshal(src, &root) == nil

	out := make([]Problem, len(raw))
	for i, r := range raw {
		p := Problem{Path: pathString(r.tokens), Message: r.message}
		if haveNodes {
			p.Line = lineForTokens(&root, r.tokens)
		}
		out[i] = p
	}
	return out
}
