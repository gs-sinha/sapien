package flow

import (
	"sort"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/expr"
)

// templates extracts the inner text of every `${...}` interpolation in s,
// mirroring internal/expr's own template splitting (brace/quote-aware, with
// `$${` escaping to a literal `${`) since that logic isn't exported. An
// unterminated `${` is simply not reported as a template (best effort);
// nothing in this package relies on it being caught here specifically,
// since a step's other checks don't depend on it.
func templates(s string) []string {
	var out []string
	n := len(s)
	i := 0
	for i < n {
		if s[i] == '$' && i+2 < n && s[i+1] == '$' && s[i+2] == '{' {
			i += 3
			continue
		}
		if s[i] == '$' && i+1 < n && s[i+1] == '{' {
			start := i + 2
			j := start
			depth := 1
			var quote byte
			for j < n && depth > 0 {
				c := s[j]
				if quote != 0 {
					if c == '\\' && j+1 < n {
						j += 2
						continue
					}
					if c == quote {
						quote = 0
					}
					j++
					continue
				}
				switch c {
				case '\'', '"':
					quote = c
					j++
				case '{':
					depth++
					j++
				case '}':
					depth--
					j++
				default:
					j++
				}
			}
			if depth == 0 {
				out = append(out, s[start:j-1])
			}
			i = j
			continue
		}
		i++
	}
	return out
}

// collectTemplates walks v (a decoded YAML value: map[string]any, []any,
// string, or a scalar) and returns every `${...}` template expression found
// in a string leaf, in a deterministic (map-key-sorted) order.
func collectTemplates(v any) []string {
	var out []string
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			out = append(out, templates(t)...)
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k])
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		default:
			// numbers, bools, nil: nothing to interpolate.
		}
	}
	walk(v)
	return out
}

// assertionTemplates collects every `${...}` template inside a structured
// assertion's comparison values -- eq, neq, contains, and matches -- the
// same way collectTemplates does for step input/body/headers, so a
// template there gets the same EXPR_SYNTAX/UNKNOWN_STEP/STEP_ORDER/
// UNKNOWN_FIELD checks as everywhere else (PLAN §8).
func assertionTemplates(a domain.Assertion) []string {
	var out []string
	out = append(out, collectTemplates(a.Eq)...)
	out = append(out, collectTemplates(a.Neq)...)
	out = append(out, collectTemplates(a.Contains)...)
	out = append(out, templates(a.Matches)...)
	return out
}

// stringMapToAny converts a map[string]string (Step.Headers,
// ExplicitParams.Headers) to map[string]any for collectTemplates.
func stringMapToAny(m map[string]string) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ReferencedSteps returns the distinct step ids st's expressions (input,
// params, body, headers, until, extract, assert) reference via
// `steps.<id>...`, in the order first seen. Used by the runner to detect a
// reference to a step that has no data at run time -- e.g. an earlier step
// skipped by `from_step` without a resumed run to seed it from (PLAN §9).
func ReferencedSteps(st domain.Step) []string {
	var texts []string
	texts = append(texts, collectTemplates(st.Input)...)
	if st.Params != nil {
		texts = append(texts, collectTemplates(st.Params.Path)...)
		texts = append(texts, collectTemplates(st.Params.Query)...)
		texts = append(texts, collectTemplates(stringMapToAny(st.Params.Headers))...)
	}
	texts = append(texts, collectTemplates(st.Body)...)
	texts = append(texts, collectTemplates(stringMapToAny(st.Headers))...)
	if st.Until != "" {
		texts = append(texts, st.Until)
	}
	if len(st.Extract) > 0 {
		names := make([]string, 0, len(st.Extract))
		for name := range st.Extract {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			texts = append(texts, st.Extract[name])
		}
	}
	for _, a := range st.Assert {
		if a.Expr != "" {
			texts = append(texts, a.Expr)
		} else {
			texts = append(texts, assertionTemplates(a)...)
		}
	}

	seen := map[string]bool{}
	var out []string
	for _, t := range texts {
		for _, ref := range expr.Roots(t) {
			if ref.Root == "steps" && ref.StepID != "" && !seen[ref.StepID] {
				seen[ref.StepID] = true
				out = append(out, ref.StepID)
			}
		}
	}
	return out
}
