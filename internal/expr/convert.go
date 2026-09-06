package expr

import (
	"encoding/json"
	"math"
	"reflect"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
)

// normalizeJSON recursively converts v so that integral float64 values
// become int64, json.Number values are parsed to int64 (or float64 if
// fractional), and everything else is left as-is. This makes values decoded
// from encoding/json behave predictably in CEL, e.g. `body.count == 2`.
func normalizeJSON(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case float64:
		if isIntegral(t) {
			return int64(t)
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalizeJSON(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeJSON(val)
		}
		return out
	default:
		return v
	}
}

func isIntegral(f float64) bool {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return false
	}
	if f < -9.007199254740992e15 || f > 9.007199254740992e15 {
		// Outside the range where float64 can represent every integer
		// exactly; keep as a double rather than risk a lossy conversion.
		return false
	}
	return f == math.Trunc(f)
}

// toGoValue converts a CEL result value back to a plain Go value: maps become
// map[string]any, lists become []any, null becomes nil, and scalars use their
// natural Go representation (int64, float64, string, bool).
func toGoValue(v ref.Val) (any, error) {
	if v == nil || v == types.NullValue {
		return nil, nil
	}
	if _, ok := v.(traits.Mapper); ok {
		native, err := v.ConvertToNative(reflect.TypeOf(map[string]any{}))
		if err != nil {
			return nil, err
		}
		return normalizeJSON(native), nil
	}
	if _, ok := v.(traits.Lister); ok {
		native, err := v.ConvertToNative(reflect.TypeOf([]any{}))
		if err != nil {
			return nil, err
		}
		return normalizeJSON(native), nil
	}
	if types.IsError(v) {
		return nil, v.(*types.Err)
	}
	return v.Value(), nil
}
