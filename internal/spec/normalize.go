package spec

import (
	"fmt"
	"time"
)

// normalizeYAML converts a value decoded by yaml.v3 into `any` (as done by
// yaml.Unmarshal(src, &v)) into a tree of the types the jsonschema/v6
// validator understands: map[string]any, []any, string, bool, nil, and the
// native Go numeric types (which the validator already accepts directly).
//
// yaml.v3 mostly matches encoding/json's shape (it even decodes mappings
// into map[string]any, unlike yaml.v2's map[interface{}]interface{}), but it
// has one surprising divergence that matters here: an unquoted scalar that
// looks like a timestamp (e.g. `2026-09-05T10:20:00Z`, used throughout the
// memory and environment schemas) decodes to time.Time, not string. A
// time.Time reaching the validator has no matching jsonschema type (it is
// neither a Go string nor a supported number/map/slice/bool), so every
// schema node above it -- even one with no "type" keyword at all -- fails
// with an opaque "invalid jsonType time.Time" error. normalizeYAML re-renders
// any time.Time back into the RFC3339 string it came from so "format":
// "date-time" validates the way an author would expect.
func normalizeYAML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = normalizeYAML(vv)
		}
		return out
	case map[any]any:
		// Defensive: gopkg.in/yaml.v3 does not produce this for `any` targets
		// (unlike yaml.v2), but guard against it if that ever changes.
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[fmt.Sprint(k)] = normalizeYAML(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = normalizeYAML(vv)
		}
		return out
	case time.Time:
		return t.Format(time.RFC3339)
	default:
		// string, bool, nil, int, int64, float64, ... already understood.
		return v
	}
}
