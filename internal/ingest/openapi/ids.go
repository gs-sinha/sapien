package openapi

import (
	"strconv"
	"strings"
)

// synthesizeOpID builds a synthesized operation id from an HTTP method and path,
// per PLAN §5: "<method>_<path-slug>", method lower-cased, leading slash removed,
// "{param}" -> "param", "/" and any non [A-Za-z0-9_] byte -> "_".
//
// Example: GET /v1/allocations/stats -> get_v1_allocations_stats
//
//	GET /v1/riders/{riderId}  -> get_v1_riders_riderId
func synthesizeOpID(method, path string) string {
	p := strings.TrimPrefix(path, "/")
	p = strings.ReplaceAll(p, "{", "")
	p = strings.ReplaceAll(p, "}", "")

	var b strings.Builder
	b.Grow(len(p) + len(method) + 1)
	b.WriteString(strings.ToLower(method))
	b.WriteByte('_')
	for i := 0; i < len(p); i++ {
		c := p[i]
		if isSlugByte(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func isSlugByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// jsonPointerEscape escapes a JSON pointer reference token per RFC 6901:
// "~" -> "~0", then "/" -> "~1".
func jsonPointerEscape(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

// operationPointer builds the JSON pointer for an operation: /paths/<escaped path>/<method>.
func operationPointer(path, method string) string {
	return "/paths/" + jsonPointerEscape(path) + "/" + strings.ToLower(method)
}

// dedupeID returns a final, collision-free id given the final ids already assigned so
// far (used), applying the "_2", "_3", ... suffixing convention. It reports whether a
// collision occurred, in which case a DUPLICATE_OPERATION_ID warning should be emitted.
func dedupeID(id string, used map[string]bool) (final string, collided bool) {
	if !used[id] {
		used[id] = true
		return id, false
	}
	for n := 2; ; n++ {
		candidate := id + "_" + strconv.Itoa(n)
		if !used[candidate] {
			used[candidate] = true
			return candidate, true
		}
	}
}
