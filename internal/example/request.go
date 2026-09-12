package example

import (
	"fmt"
	"sort"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
)

// maxSynthDepth bounds schema recursion; a genuine cycle is rendered as {}.
const maxSynthDepth = 8

// Resolve returns the best ready-to-send request for op. Precedence: a
// verified saved example (it really was sent and really worked), then a
// hand-written saved example, then the contract's own `example:`, then a
// payload synthesized from the request schema. saved is ForOperations'
// result for this operation (verified first); anything for another operation
// is ignored.
//
// This exists because knowing an operation's schema is not the same as
// knowing what a call to it looks like: agents were reading the schema from
// Sapien and then assembling payloads in throwaway scripts, and a human in
// the UI had to compile a body field by field from the schema panel. One
// resolver serves get_api, the HTTP API, and the UI so all three answer that
// question the same way.
func Resolve(op *domain.Operation, saved []domain.SavedExample) domain.RequestExample {
	if op == nil {
		return domain.RequestExample{}
	}

	for _, want := range []bool{true, false} { // verified first, then hand-written
		for _, ex := range saved {
			if ex.Operation != op.ID || (ex.Verified != nil) != want {
				continue
			}
			out := domain.RequestExample{
				Operation: op.ID,
				Source:    domain.RequestExampleSaved,
				SourceID:  ex.ID,
				Input:     ex.Input,
				Body:      ex.Body,
				Headers:   ex.Headers,
				Note:      "hand-written; not yet confirmed against a running service",
			}
			if want {
				out.Source = domain.RequestExampleVerified
				out.Note = verifiedNote(ex)
			}
			return out
		}
	}

	if ex, ok := contractExample(op); ok {
		return domain.RequestExample{
			Operation: op.ID,
			Source:    domain.RequestExampleContract,
			SourceID:  ex.Name,
			Input:     SynthesizeInput(op, false),
			Body:      ex.Value,
			Note:      "never sent; params below are placeholders",
		}
	}

	return Synthesize(op, false)
}

// Synthesize builds a request from op's schema alone, ignoring every saved and
// contract example. includeOptional fills in every field the schema declares
// rather than the required ones plus those the contract gives a value for.
//
// This is what the UI's "whole shape" button asks for: a reader who wants to
// see the entire payload and delete from it is not served by being handed the
// contract's own example again, which is what Resolve would return.
func Synthesize(op *domain.Operation, includeOptional bool) domain.RequestExample {
	if op == nil {
		return domain.RequestExample{}
	}
	out := domain.RequestExample{
		Operation: op.ID,
		Source:    domain.RequestExampleSynthesized,
		Input:     SynthesizeInput(op, includeOptional),
		Note:      synthesizedNote(includeOptional),
	}
	if op.RequestBody != nil && op.RequestBody.Schema != nil {
		out.Body = SynthesizeBody(op.RequestBody.Schema, includeOptional)
	}
	return out
}

// verifiedNote describes when and where a verified example was proven, since
// "verified" against a local stub two months ago is worth less than
// "verified" against staging this morning.
func verifiedNote(ex domain.SavedExample) string {
	if ex.Verified == nil {
		return ""
	}
	switch {
	case ex.Verified.Env != "" && !ex.Verified.At.IsZero():
		return fmt.Sprintf("sent successfully against %s on %s", ex.Verified.Env, ex.Verified.At.Format(time.DateOnly))
	case ex.Verified.Env != "":
		return "sent successfully against " + ex.Verified.Env
	default:
		return "sent successfully in a recorded run"
	}
}

func synthesizedNote(includeOptional bool) string {
	if includeOptional {
		return "every field the schema declares; every value is a placeholder, never sent"
	}
	return "required fields plus any the contract gives a value for; every value is a placeholder, never sent"
}

// contractExample returns the request body example the contract itself
// declares (openapi `example:`/`examples:` under requestBody content), which
// is the authored home for an operation's payload: it travels with the code
// and every other OpenAPI tool shows it too.
func contractExample(op *domain.Operation) (domain.Example, bool) {
	if op.RequestBody == nil {
		return domain.Example{}, false
	}
	for _, ex := range op.RequestBody.Examples {
		if ex.Value != nil {
			return ex, true
		}
	}
	return domain.Example{}, false
}

// SynthesizeInput builds the `input:` map for op's params: every required
// param, plus optional ones the contract gives a real value for (an
// example, a default, or an enum). includeOptional adds the rest with
// placeholder values.
func SynthesizeInput(op *domain.Operation, includeOptional bool) map[string]any {
	out := map[string]any{}
	for _, p := range op.Params {
		if p.Deprecated && !p.Required && !includeOptional {
			// A deprecated param that is nonetheless required still has to be
			// sent; leaving it out would hand back a request that cannot work.
			continue
		}
		v, known := paramValue(p)
		if !p.Required && !known && !includeOptional {
			continue
		}
		out[p.Name] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// paramValue returns a param's value and whether the contract actually
// supplied it (rather than it being a placeholder derived from the type).
func paramValue(p domain.Param) (value any, known bool) {
	if p.Example != nil {
		return p.Example, true
	}
	if p.Schema != nil {
		if p.Schema.Default != nil {
			return p.Schema.Default, true
		}
		if len(p.Schema.Enum) > 0 {
			return p.Schema.Enum[0], true
		}
	}
	return placeholder(p.Schema, p.Name), false
}

// SynthesizeBody builds a payload from a request body schema: required
// fields always, optional fields only when the contract gives them a value
// (example, default, or enum) unless includeOptional is set, in which case
// every field is present with a placeholder. Required-by-default keeps the
// result sendable; includeOptional is for a human who wants the whole shape
// in front of them to delete from.
func SynthesizeBody(schema *domain.Schema, includeOptional bool) any {
	return synthesize(schema, "", includeOptional, 0)
}

func synthesize(schema *domain.Schema, name string, includeOptional bool, depth int) any {
	if schema == nil || depth > maxSynthDepth {
		return nil
	}
	switch schema.Kind {
	case domain.KindObject:
		return synthesizeObject(schema, includeOptional, depth)
	case domain.KindArray:
		return []any{synthesize(schema.Items, "item", includeOptional, depth+1)}
	case domain.KindOneOf, domain.KindAnyOf:
		if len(schema.Variants) > 0 {
			return synthesize(schema.Variants[0], name, includeOptional, depth+1)
		}
		return nil
	case domain.KindAllOf:
		merged := map[string]any{}
		for _, v := range schema.Variants {
			if m, ok := synthesize(v, name, includeOptional, depth+1).(map[string]any); ok {
				for k, val := range m {
					merged[k] = val
				}
			}
		}
		return merged
	case domain.KindRef:
		// A ref survives ingest only for a genuine cycle; there is nothing
		// left to expand.
		return map[string]any{}
	default:
		return placeholder(schema, name)
	}
}

func synthesizeObject(schema *domain.Schema, includeOptional bool, depth int) any {
	required := make(map[string]bool, len(schema.Required))
	for _, r := range schema.Required {
		required[r] = true
	}
	order := schema.PropertyOrder
	if len(order) == 0 {
		order = sortedKeys(schema.Properties)
	}
	out := map[string]any{}
	for _, key := range order {
		prop, ok := schema.Properties[key]
		if !ok || prop == nil {
			continue
		}
		if prop.ReadOnly {
			continue // server-owned; sending it back is noise at best
		}
		if !required[key] && !includeOptional && !hasContractValue(prop) {
			continue
		}
		out[key] = synthesize(prop, key, includeOptional, depth+1)
	}
	return out
}

// hasContractValue reports whether the contract pins this field's value
// (rather than leaving a caller to invent one), which is what makes an
// optional field worth including in a payload nobody asked to be complete.
func hasContractValue(s *domain.Schema) bool {
	return s != nil && (s.Example != nil || s.Default != nil || len(s.Enum) > 0)
}

// placeholder is the leaf value for a scalar: whatever the contract says,
// else something recognisable for the type. A string leaf carries its own
// field name, which reads better in a payload a human is about to edit than
// a bare "string" repeated a dozen times.
func placeholder(schema *domain.Schema, name string) any {
	if schema == nil {
		return nil
	}
	if schema.Example != nil {
		return schema.Example
	}
	if schema.Default != nil {
		return schema.Default
	}
	if len(schema.Enum) > 0 {
		return schema.Enum[0]
	}
	switch schema.Kind {
	case domain.KindString:
		switch schema.Format {
		case "date-time":
			return "2026-01-01T00:00:00Z"
		case "date":
			return "2026-01-01"
		case "email":
			return "user@example.com"
		case "uuid":
			return "00000000-0000-0000-0000-000000000000"
		case "uri", "url":
			return "https://example.com"
		default:
			if name != "" {
				return "<" + name + ">"
			}
			return "string"
		}
	case domain.KindInteger, domain.KindNumber:
		return 0
	case domain.KindBoolean:
		return false
	default:
		return nil
	}
}

func sortedKeys(m map[string]*domain.Schema) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
