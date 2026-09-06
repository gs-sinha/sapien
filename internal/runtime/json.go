package runtime

import (
	"bytes"
	"encoding/json"
)

// ParseJSONBody parses b as JSON, preserving integer precision: numbers are
// decoded with json.Decoder.UseNumber and converted to int64 when the
// literal parses as an integer, otherwise float64. It reports (nil, false)
// when b is empty or is not a single valid JSON value.
func ParseJSONBody(b []byte) (any, bool) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if dec.More() {
		// trailing content after the first JSON value
		return nil, false
	}
	return normalizeNumbers(v), true
}

// normalizeNumbers walks a decoded JSON value converting json.Number leaves
// to int64 (when integral) or float64.
func normalizeNumbers(v any) any {
	switch t := v.(type) {
	case json.Number:
		return numberToValue(t)
	case map[string]any:
		for k, vv := range t {
			t[k] = normalizeNumbers(vv)
		}
		return t
	case []any:
		for i, vv := range t {
			t[i] = normalizeNumbers(vv)
		}
		return t
	default:
		return v
	}
}

func numberToValue(n json.Number) any {
	if i, err := n.Int64(); err == nil {
		return i
	}
	f, _ := n.Float64()
	return f
}

// encodeBody turns a Request.Body into wire bytes. isJSON reports whether
// body was JSON-encoded here (as opposed to sent verbatim as []byte/string).
func encodeBody(body any) (data []byte, isJSON bool, err error) {
	switch b := body.(type) {
	case nil:
		return nil, false, nil
	case []byte:
		return b, false, nil
	case string:
		return []byte(b), false, nil
	default:
		data, err = json.Marshal(b)
		return data, true, err
	}
}

// deepCopyJSON deep-copies a value produced by ParseJSONBody so redaction
// never mutates a caller's live value.
func deepCopyJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = deepCopyJSON(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = deepCopyJSON(vv)
		}
		return out
	default:
		// strings, int64, float64, bool, nil are immutable value types.
		return v
	}
}
