package runtime

import (
	"net/http"
	"sort"
	"strings"

	"github.com/growsimplee/sapien/internal/domain"
)

const redactedPlaceholder = "[REDACTED]"

// defaultRedactedHeaders are always scrubbed, regardless of environment
// configuration (PLAN §19, §28).
var defaultRedactedHeaders = []string{
	"authorization",
	"proxy-authorization",
	"cookie",
	"set-cookie",
	"x-api-key",
	"x-auth-token",
	"x-amz-security-token",
}

// pathSeg is one segment of a parsed JSON redaction path.
type pathSeg struct {
	name    string
	isArray bool // segment was written as name[] (wildcard over an array)
}

// Redactor scrubs secret values and configured headers/JSON paths from
// requests and responses before they are persisted (PLAN §19, §28).
type Redactor struct {
	headers      map[string]struct{}
	jsonPaths    [][]pathSeg
	secretValues []string
}

// NewRedactor builds a Redactor from an environment's Redaction config (may
// be nil) and the resolved secret values that must never appear at rest.
// secretValues shorter than 4 characters are ignored (too likely to produce
// false-positive matches).
func NewRedactor(cfg *domain.Redaction, secretValues []string) *Redactor {
	r := &Redactor{headers: map[string]struct{}{}}
	for _, h := range defaultRedactedHeaders {
		r.headers[h] = struct{}{}
	}
	if cfg != nil {
		for _, h := range cfg.Headers {
			r.headers[strings.ToLower(h)] = struct{}{}
		}
		for _, p := range cfg.JSONPaths {
			r.jsonPaths = append(r.jsonPaths, parseJSONPath(p))
		}
	}
	for _, v := range secretValues {
		if len(v) >= 4 {
			r.secretValues = append(r.secretValues, v)
		}
	}
	// Longest-first avoids a shorter secret value shadowing part of a
	// longer one that contains it.
	sort.Slice(r.secretValues, func(i, j int) bool { return len(r.secretValues[i]) > len(r.secretValues[j]) })
	return r
}

func parseJSONPath(path string) []pathSeg {
	parts := strings.Split(path, ".")
	segs := make([]pathSeg, 0, len(parts))
	for _, p := range parts {
		if strings.HasSuffix(p, "[]") {
			segs = append(segs, pathSeg{name: strings.TrimSuffix(p, "[]"), isArray: true})
		} else {
			segs = append(segs, pathSeg{name: p})
		}
	}
	return segs
}

// String scrubs every occurrence of a known secret value out of s.
func (r *Redactor) String(s string) string {
	for _, v := range r.secretValues {
		if v == "" {
			continue
		}
		s = strings.ReplaceAll(s, v, redactedPlaceholder)
	}
	return s
}

func (r *Redactor) isRedactedHeader(name string) bool {
	_, ok := r.headers[strings.ToLower(name)]
	return ok
}

// redactHeaders scrubs h and normalizes keys to canonical form (e.g.
// "cookie" -> "Cookie") so records have a stable casing regardless of how
// the caller supplied the header name.
func (r *Redactor) redactHeaders(h map[string]string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		ck := http.CanonicalHeaderKey(k)
		if r.isRedactedHeader(k) {
			out[ck] = redactedPlaceholder
		} else {
			out[ck] = r.String(v)
		}
	}
	return out
}

// redactSecretsInValue recursively scrubs secret values out of every string
// leaf of v (a value produced by ParseJSONBody / deepCopyJSON). It mutates
// and returns v.
func (r *Redactor) redactSecretsInValue(v any) any {
	switch t := v.(type) {
	case string:
		return r.String(t)
	case map[string]any:
		for k, vv := range t {
			t[k] = r.redactSecretsInValue(vv)
		}
		return t
	case []any:
		for i, vv := range t {
			t[i] = r.redactSecretsInValue(vv)
		}
		return t
	default:
		return t
	}
}

// applyJSONPath sets the value(s) matched by segs to redactedPlaceholder.
// It mutates and returns v.
func applyJSONPath(v any, segs []pathSeg) any {
	if len(segs) == 0 {
		return redactedPlaceholder
	}
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	seg := segs[0]
	child, exists := m[seg.name]
	if !exists {
		return v
	}
	rest := segs[1:]
	if seg.isArray {
		arr, ok := child.([]any)
		if !ok {
			return v
		}
		for i, item := range arr {
			if len(rest) == 0 {
				arr[i] = redactedPlaceholder
			} else {
				arr[i] = applyJSONPath(item, rest)
			}
		}
		m[seg.name] = arr
		return v
	}
	if len(rest) == 0 {
		m[seg.name] = redactedPlaceholder
	} else {
		m[seg.name] = applyJSONPath(child, rest)
	}
	return v
}

func (r *Redactor) redactBody(v any) any {
	body := deepCopyJSON(v)
	body = r.redactSecretsInValue(body)
	for _, segs := range r.jsonPaths {
		body = applyJSONPath(body, segs)
	}
	return body
}

// RequestRecord produces the redacted, persistable form of req. BodyRaw is
// populated only when Body was not JSON-encoded (nil, []byte, or string);
// otherwise Body carries the parsed-and-redacted JSON.
func (r *Redactor) RequestRecord(req Request) domain.RequestRecord {
	rec := domain.RequestRecord{
		Method:  req.Method,
		URL:     r.String(req.URL),
		Headers: r.redactHeaders(req.Headers),
	}

	bodyBytes, isJSON, err := encodeBody(req.Body)
	if err != nil || !isJSON {
		if len(bodyBytes) > 0 {
			rec.BodyRaw = r.String(string(bodyBytes))
		}
		return rec
	}

	parsed, ok := ParseJSONBody(bodyBytes)
	if !ok {
		rec.BodyRaw = r.String(string(bodyBytes))
		return rec
	}
	rec.Body = r.redactBody(parsed)
	return rec
}

// ResponseRecord produces the redacted, persistable form of resp. BodyRaw is
// populated only when Body is nil (non-JSON or truncated); otherwise Body
// carries a redacted deep copy, and the live resp is never mutated.
func (r *Redactor) ResponseRecord(resp *Response) domain.ResponseRecord {
	rec := domain.ResponseRecord{
		Status:    resp.Status,
		Headers:   r.redactHeaders(resp.Headers),
		Truncated: resp.Truncated,
		Size:      resp.Size,
	}
	if resp.Body == nil {
		if len(resp.BodyRaw) > 0 {
			rec.BodyRaw = r.String(string(resp.BodyRaw))
		}
		return rec
	}
	rec.Body = r.redactBody(resp.Body)
	return rec
}
