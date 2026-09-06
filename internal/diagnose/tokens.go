package diagnose

import "strings"

// candidateKeys are the response-body field names diagnose looks at when
// pulling search tokens out of a failed step's response body. The order is
// the deterministic order tokens are discovered in (map iteration order in
// Go is randomized, so extractTokens always walks this fixed list instead
// of ranging over the parsed body directly).
var candidateKeys = []string{
	"code", "error", "error_code", "errorCode", "type", "reason",
	"title", "message", "detail", "details",
}

// maxTokenLen and minTokenLen bound one extracted token: longer values are
// truncated (they are search queries, not stored verbatim), shorter ones
// are dropped as too generic to search on usefully.
const (
	maxTokenLen = 120
	minTokenLen = 3
)

// extractTokens pulls candidate search tokens out of a parsed JSON response
// body: the string value of every key in candidateKeys, and, when that
// value is itself an object or array, the string values reachable one
// level inside it -- so {"error":{"code":"X","message":"Y"}} yields both
// "X" and "Y", and {"details":[{"code":"X"}]} yields "X". Tokens are
// trimmed, capped at maxTokenLen, dropped if shorter than minTokenLen, and
// deduplicated, preserving discovery order. body that is not a JSON object
// (nil, a raw string, a bare array, ...) yields no tokens.
func extractTokens(body any) []string {
	m, ok := body.(map[string]any)
	if !ok {
		return nil
	}

	var tokens []string
	seen := make(map[string]bool)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if len(s) > maxTokenLen {
			s = s[:maxTokenLen]
		}
		if len(s) < minTokenLen || seen[s] {
			return
		}
		seen[s] = true
		tokens = append(tokens, s)
	}

	// oneLevelDown extracts the string values reachable one level inside v:
	// a nested object's own candidate-key values, or a nested array's
	// string (or candidate-keyed object) elements.
	oneLevelDown := func(v any) {
		switch vv := v.(type) {
		case map[string]any:
			for _, k := range candidateKeys {
				if s, ok := vv[k].(string); ok {
					add(s)
				}
			}
		case []any:
			for _, item := range vv {
				switch iv := item.(type) {
				case string:
					add(iv)
				case map[string]any:
					for _, k := range candidateKeys {
						if s, ok := iv[k].(string); ok {
							add(s)
						}
					}
				}
			}
		}
	}

	for _, k := range candidateKeys {
		val, ok := m[k]
		if !ok {
			continue
		}
		if s, ok := val.(string); ok {
			add(s)
			continue
		}
		oneLevelDown(val)
	}
	return tokens
}

// dedupeStrings returns in with duplicates removed, preserving the order of
// first appearance.
func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
