// Package textutil holds small, dependency-free text helpers shared by the
// catalog indexer (writer) and the search layer (reader), so both tokenize
// identically. Keep it tiny and stable: several packages depend on it.
package textutil

import (
	"net/url"
	"strings"
	"unicode"
)

// SplitIdent splits an identifier on camelCase, snake_case, kebab-case, dots,
// and letter/digit boundaries, returning lower-cased parts.
// "getRiderById" -> [get rider by id]; "qcomSkill" -> [qcom skill]; "v1" -> [v1].
func SplitIdent(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == '.' || r == '/' || r == ' ' || r == '{' || r == '}' || r == '[' || r == ']':
			flush()
		case unicode.IsUpper(r):
			// boundary before an upper-case letter, unless we are inside an acronym run
			// ("HTTPServer" -> http server) handled by peeking at the next rune.
			if len(cur) > 0 {
				prev := rs[i-1]
				nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
					flush()
				}
			}
			cur = append(cur, r)
		case unicode.IsDigit(r):
			// keep "v1" together but split "orders2" -> orders 2? No: keep letters+digits together
			// when digits follow letters (v1, s3); split when letters follow digits (2fa -> 2 fa).
			cur = append(cur, r)
		default:
			if len(cur) > 0 && unicode.IsDigit(rs[i-1]) && unicode.IsLetter(r) && !(len(cur) > 1 && unicode.IsLetter(cur[0])) {
				flush()
			}
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

// PathTokens tokenizes an HTTP path template for indexing: each raw segment
// (braces stripped) plus its identifier parts, de-duplicated, lower-cased.
// "/v1/riders/{riderId}" -> [v1 riders riderid rider id].
func PathTokens(path string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.ToLower(t)
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, seg := range strings.Split(path, "/") {
		seg = strings.Trim(seg, "{}")
		if seg == "" {
			continue
		}
		add(seg)
		for _, p := range SplitIdent(seg) {
			add(p)
		}
	}
	return out
}

// Tokens tokenizes free text or identifiers for indexing: words split on
// non-alphanumerics, each further split as an identifier; de-duplicated,
// lower-cased, order preserved.
func Tokens(s string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	word := []rune{}
	flushWord := func() {
		if len(word) == 0 {
			return
		}
		w := string(word)
		word = word[:0]
		add(strings.ToLower(w))
		parts := SplitIdent(w)
		if len(parts) > 1 {
			for _, p := range parts {
				add(p)
			}
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			word = append(word, r)
		} else {
			flushWord()
		}
	}
	flushWord()
	return out
}

// Join joins tokens with single spaces (the form stored in FTS columns).
func Join(tokens []string) string { return strings.Join(tokens, " ") }

// IsHTTPMethod reports whether s (any case) is an HTTP method name.
func IsHTTPMethod(s string) bool {
	switch strings.ToUpper(s) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT":
		return true
	}
	return false
}

// ParseMethodPath recognizes queries like "POST /v1/orders" or "/v1/orders".
// It returns the upper-cased method ("" when absent), the path, and ok=false
// when the query is not path-shaped.
func ParseMethodPath(q string) (method, path string, ok bool) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", "", false
	}
	parts := strings.Fields(q)
	if len(parts) == 2 && IsHTTPMethod(parts[0]) && strings.HasPrefix(parts[1], "/") {
		return strings.ToUpper(parts[0]), parts[1], true
	}
	if len(parts) == 1 && strings.HasPrefix(parts[0], "/") {
		return "", parts[0], true
	}
	return "", "", false
}

// PathMatches reports whether a concrete or templated path matches a template,
// treating {param} segments in the template as wildcards for one segment.
// PathMatches("/v1/riders/{riderId}", "/v1/riders/R123") == true.
func PathMatches(template, path string) bool {
	ts := strings.Split(strings.Trim(template, "/"), "/")
	ps := strings.Split(strings.Trim(path, "/"), "/")
	if len(ts) != len(ps) {
		return false
	}
	for i := range ts {
		if strings.HasPrefix(ts[i], "{") && strings.HasSuffix(ts[i], "}") {
			if ps[i] == "" {
				return false
			}
			continue
		}
		if !strings.EqualFold(ts[i], ps[i]) {
			return false
		}
	}
	return true
}

// ParseURLQuery recognises every path-shaped query a person or agent is
// likely to paste: "POST /v1/orders", "/v1/orders?x=1", "v1/orders/123",
// "https://api.example.com/base/v1/orders#frag", or "GET https://...". It
// returns the upper-cased method ("" when absent), the path with any
// scheme, host, query string, and fragment removed and a leading slash
// ensured, and ok=false when the query is not path-shaped. Operation ids
// such as "order-service.createOrder" are not path-shaped (no slash).
func ParseURLQuery(q string) (method, path string, ok bool) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", "", false
	}
	parts := strings.Fields(q)
	switch {
	case len(parts) == 2 && IsHTTPMethod(parts[0]):
		method, q = strings.ToUpper(parts[0]), parts[1]
	case len(parts) == 1:
		q = parts[0]
	default:
		return "", "", false
	}

	lower := strings.ToLower(q)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		u, err := url.Parse(q)
		if err != nil {
			return "", "", false
		}
		q = u.Path
		if q == "" {
			q = "/"
		}
	} else if !strings.HasPrefix(q, "/") {
		if !strings.Contains(q, "/") {
			return "", "", false
		}
		q = "/" + q
	}

	if i := strings.IndexAny(q, "?#"); i >= 0 {
		q = q[:i]
	}
	for strings.Contains(q, "//") {
		q = strings.ReplaceAll(q, "//", "/")
	}
	if len(q) > 1 {
		q = strings.TrimRight(q, "/")
	}
	if q == "" {
		q = "/"
	}
	return method, q, true
}

// PathSegments splits a path into its non-empty segments.
func PathSegments(p string) []string {
	var out []string
	for _, s := range strings.Split(strings.Trim(p, "/"), "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// IsPathParam reports whether a template segment is a {param} placeholder.
func IsPathParam(seg string) bool {
	return len(seg) > 2 && strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")
}
