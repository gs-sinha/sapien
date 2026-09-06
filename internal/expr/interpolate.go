package expr

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/growsimplee/sapien/internal/errs"
)

// templatePart is one piece of a split template: either literal text, or the
// inner text of a ${...} expression.
type templatePart struct {
	text   string
	isExpr bool
}

// IsTemplate reports whether s contains at least one ${...} interpolation
// (as opposed to only escaped $${ sequences or no `${` at all).
func IsTemplate(s string) bool {
	parts, err := splitTemplate(s)
	if err != nil {
		return strings.Contains(s, "${")
	}
	for _, p := range parts {
		if p.isExpr {
			return true
		}
	}
	return false
}

// splitTemplate splits tmpl into literal and ${...} expression parts, aware
// of quoted strings (so a `}` inside a CEL string literal doesn't end the
// expression early) and of the `$${` escape for a literal `${`.
func splitTemplate(tmpl string) ([]templatePart, error) {
	var parts []templatePart
	var lit strings.Builder
	n := len(tmpl)
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, templatePart{text: lit.String()})
			lit.Reset()
		}
	}
	i := 0
	for i < n {
		if tmpl[i] == '$' && i+2 < n && tmpl[i+1] == '$' && tmpl[i+2] == '{' {
			lit.WriteString("${")
			i += 3
			continue
		}
		if tmpl[i] == '$' && i+1 < n && tmpl[i+1] == '{' {
			flush()
			start := i + 2
			j := start
			depth := 1
			var quote byte
		scan:
			for j < n {
				c := tmpl[j]
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
					if depth == 0 {
						break scan
					}
				default:
					j++
				}
			}
			if depth != 0 {
				return nil, errs.New(errs.Expr, "unterminated `${` in template").WithDetail("template", tmpl)
			}
			parts = append(parts, templatePart{text: tmpl[start : j-1], isExpr: true})
			i = j
			continue
		}
		lit.WriteByte(tmpl[i])
		i++
	}
	flush()
	return parts, nil
}

var secretRe = regexp.MustCompile(`\bsecret\.([A-Za-z_][A-Za-z0-9_]*)\b`)

// resolveSecrets substitutes every secret.NAME reference in text with a CEL
// string literal of its resolved value, so the caller can then compile/eval
// text as ordinary CEL with no "secret" root involved. It requires
// s.AllowSecrets and s.SecretResolver.
func resolveSecrets(text string, s Scope) (string, []string, error) {
	idxs := secretRe.FindAllStringSubmatchIndex(text, -1)
	if len(idxs) == 0 {
		return text, nil, nil
	}
	if !s.AllowSecrets {
		return "", nil, errs.New(errs.Expr, "secret references are only allowed in headers and auth values").
			WithDetail("expr", text)
	}
	var b strings.Builder
	var used []string
	last := 0
	for _, m := range idxs {
		start, end := m[0], m[1]
		name := text[m[2]:m[3]]
		b.WriteString(text[last:start])
		if s.SecretResolver == nil {
			return "", nil, errs.New(errs.Expr, "secret resolver not configured").WithDetail("secret", name)
		}
		val, err := s.SecretResolver(name)
		if err != nil {
			return "", nil, errs.Wrap(errs.SecretMissing, err, "resolving secret `%s`", name).WithDetail("secret", name)
		}
		used = append(used, name)
		b.WriteString(celStringLiteral(val))
		last = end
	}
	b.WriteString(text[last:])
	return b.String(), used, nil
}

// Interpolate evaluates a ${...} template against s. A template that is
// exactly one ${expr} (with no surrounding literal text) keeps expr's native
// type; otherwise every ${expr} is stringified and concatenated with the
// surrounding literal text. Literal `$${` escapes to a `${` in the output.
func (e *Evaluator) Interpolate(tmpl string, s Scope) (any, []string, error) {
	parts, err := splitTemplate(tmpl)
	if err != nil {
		return nil, nil, err
	}
	if len(parts) == 1 && parts[0].isExpr {
		text, used, err := resolveSecrets(parts[0].text, s)
		if err != nil {
			return nil, nil, err
		}
		v, err := e.Eval(text, s)
		if err != nil {
			return nil, nil, err
		}
		return v, used, nil
	}
	var b strings.Builder
	var allUsed []string
	for _, p := range parts {
		if !p.isExpr {
			b.WriteString(p.text)
			continue
		}
		text, used, err := resolveSecrets(p.text, s)
		if err != nil {
			return nil, nil, err
		}
		allUsed = append(allUsed, used...)
		v, err := e.Eval(text, s)
		if err != nil {
			return nil, nil, err
		}
		b.WriteString(stringify(v))
	}
	return b.String(), allUsed, nil
}

// InterpolateValue walks v (typically a decoded YAML/JSON value: nested
// map[string]any / []any / string / scalars) and interpolates every string
// it finds, for step input/body/headers.
func (e *Evaluator) InterpolateValue(v any, s Scope) (any, []string, error) {
	switch t := v.(type) {
	case string:
		return e.Interpolate(t, s)
	case map[string]any:
		out := make(map[string]any, len(t))
		var used []string
		for k, val := range t {
			nv, u, err := e.InterpolateValue(val, s)
			if err != nil {
				return nil, nil, err
			}
			out[k] = nv
			used = append(used, u...)
		}
		return out, used, nil
	case []any:
		out := make([]any, len(t))
		var used []string
		for i, val := range t {
			nv, u, err := e.InterpolateValue(val, s)
			if err != nil {
				return nil, nil, err
			}
			out[i] = nv
			used = append(used, u...)
		}
		return out, used, nil
	default:
		return v, nil, nil
	}
}

// stringify renders a CEL/Go value for template concatenation: strings are
// raw, numbers use strconv (no trailing .0 for integral floats), bools use
// their usual spelling, nil becomes "", and maps/slices become compact JSON.
func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case int:
		return strconv.Itoa(t)
	case float64:
		if !math.IsInf(t, 0) && !math.IsNaN(t) && t == math.Trunc(t) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case map[string]any, []any:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	default:
		return jsonOrFmt(t)
	}
}

func jsonOrFmt(v any) string {
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return ""
}
