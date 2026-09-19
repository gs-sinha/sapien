package expr

import (
	"strings"

	"github.com/gs-sinha/sapien/internal/errs"
)

// ExpandTemplates rewrites every `${...}` template in src -- a whole
// CEL-typed field's source text (a bare `assert` string, a structured
// assertion's `expr:`, `until`, `when`, `break_when`, `foreach`,
// `repeat.until`/`repeat.while`, or an `extract` value) -- into native CEL
// syntax, so the ordinary CEL parser/checker sees every reference a
// template contains. This is the single choke point every compile path for
// those fields goes through: Evaluator.compile (runtime evaluation) and
// Parse/Roots (the flow validator's syntax check and static reference
// analysis) all call it before touching sharedEnv, so a `${...}` that used
// to hide inside an opaque CEL string literal now gets the same
// STEP_ORDER/UNKNOWN_STEP/MAYBE_SKIPPED/field checks as everywhere else.
//
// Three rewrite shapes, chosen the same way this promise reads elsewhere in
// the DSL (the same native-vs-stringified rule a structured `eq`/`neq`/...
// value already follows via Interpolate, just reached here by rewriting
// source text instead of substituting a runtime value):
//
//   - A string literal whose entire content is one template, `"${e}"` or
//     `'${e}'`, becomes `(e)` -- the quotes are dropped, so the value keeps
//     e's native type instead of becoming a string.
//   - A template embedded in a longer string literal, `"ord-${e}-x"`,
//     becomes `("ord-" + string(e) + "-x")`.
//   - A template outside any string literal, `body.ts > ${e}`, becomes
//     `body.ts > (e)`.
//
// A literal's own quote character is reused verbatim for the text around a
// template inside it -- no re-escaping is needed, since a substring of a
// validly escaped literal is itself validly escaped for the same quote
// character -- so this rewrite only ever substitutes e's own text, never
// any runtime data (that still only ever enters through CEL evaluation
// later, exactly as it did before this rewrite existed).
//
// `secret.*` stays impossible in these fields: this function does no secret
// resolution (unlike Evaluator.Interpolate), and sharedEnv never declares a
// `secret` root, so `${secret.x}` rewritten to `(secret.x)` simply fails to
// compile as an undeclared reference (see TestExpandTemplates_SecretStaysImpossible).
//
// A malformed string literal (no closing quote) is passed through
// unchanged, byte for byte, leaving that syntax error to the CEL parser,
// which reports it far more precisely than this scanner could -- including
// when a template lives inside it, so its `${...}` can legitimately survive
// this call without an error; the flow validator treats that surviving
// `${` as its own error (TEMPLATE_IN_EXPR), since nothing downstream would
// otherwise explain it. An unterminated `${` (the template body itself
// never closes) is this function's own error.
func ExpandTemplates(src string) (string, error) {
	if !strings.Contains(src, "${") {
		return src, nil
	}
	var out strings.Builder
	n := len(src)
	i := 0
	for i < n {
		c := src[i]
		switch {
		case c == '\'' || c == '"':
			consumed, err := expandStringLiteral(src, i, &out)
			if err != nil {
				return "", err
			}
			i += consumed
		case c == '$' && i+2 < n && src[i+1] == '$' && src[i+2] == '{':
			out.WriteString("${")
			i += 3
		case c == '$' && i+1 < n && src[i+1] == '{':
			body, next, err := scanTemplateBody(src, i+2)
			if err != nil {
				return "", err
			}
			out.WriteByte('(')
			out.WriteString(body)
			out.WriteByte(')')
			i = next
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String(), nil
}

// templatePiece is one piece of a string literal's content once it's been
// split at its `${...}` boundaries: either raw literal text (as originally
// written, escapes and all) or a template's inner expression text.
type templatePiece struct {
	text   string
	isExpr bool
}

// expandStringLiteral processes the CEL string literal beginning at
// src[start] (a quote character), writing its replacement to out and
// returning how many bytes of src it consumed. A literal with no `${...}`
// is copied through unchanged (including its quotes and escapes) -- this
// function, like ExpandTemplates itself, never touches a plain string. A
// malformed literal (no closing quote) is also copied through unchanged,
// leaving the syntax error to the CEL parser.
func expandStringLiteral(src string, start int, out *strings.Builder) (int, error) {
	n := len(src)
	q := src[start]
	i := start + 1
	var lit strings.Builder
	var parts []templatePiece
	sawEscape := false // a `$${` -> `${` substitution fired somewhere in this literal
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, templatePiece{text: lit.String()})
			lit.Reset()
		}
	}

	closed := false
	for i < n {
		c := src[i]
		switch {
		case c == '\\' && i+1 < n:
			lit.WriteByte(c)
			lit.WriteByte(src[i+1])
			i += 2
		case c == q:
			i++
			closed = true
		case c == '$' && i+2 < n && src[i+1] == '$' && src[i+2] == '{':
			lit.WriteString("${")
			sawEscape = true
			i += 3
		case c == '$' && i+1 < n && src[i+1] == '{':
			flush()
			body, next, err := scanTemplateBody(src, i+2)
			if err != nil {
				return 0, err
			}
			parts = append(parts, templatePiece{text: body, isExpr: true})
			i = next
		default:
			lit.WriteByte(c)
			i++
		}
		if closed {
			break
		}
	}
	flush()

	if !closed {
		// Unterminated string literal: not this function's error to raise
		// (a bare CEL syntax problem, unrelated to templating) -- pass the
		// rest through unchanged, including any `${...}` it already
		// consumed above, and let the CEL parser explain it. The flow
		// validator's TEMPLATE_IN_EXPR check is what catches a `${` that
		// survives this way.
		out.WriteString(src[start:i])
		return i - start, nil
	}

	hasExpr := false
	for _, p := range parts {
		if p.isExpr {
			hasExpr = true
			break
		}
	}
	if !hasExpr {
		if !sawEscape {
			// A plain literal, no `${...}` and no `$${` escape either:
			// copied through byte for byte, exactly as ExpandTemplates
			// promises for anything with no template in it.
			out.WriteString(src[start:i])
			return i - start, nil
		}
		// $${ was unescaped to a literal ${ somewhere, but there was no
		// real template: re-quote the now-unescaped text (flush() leaves
		// it as parts' one and only, non-expr, piece) with the same quote
		// character -- passing the ORIGINAL bytes through here would leak
		// the un-resolved `$$` into the compiled string's value.
		out.WriteByte(q)
		if len(parts) == 1 {
			out.WriteString(parts[0].text)
		}
		out.WriteByte(q)
		return i - start, nil
	}

	if len(parts) == 1 && parts[0].isExpr {
		out.WriteByte('(')
		out.WriteString(parts[0].text)
		out.WriteByte(')')
		return i - start, nil
	}

	out.WriteByte('(')
	for k, p := range parts {
		if k > 0 {
			out.WriteString(" + ")
		}
		if p.isExpr {
			out.WriteString("string(")
			out.WriteString(p.text)
			out.WriteByte(')')
		} else {
			out.WriteByte(q)
			out.WriteString(p.text)
			out.WriteByte(q)
		}
	}
	out.WriteByte(')')
	return i - start, nil
}

// scanTemplateBody scans a `${...}` template's inner expression starting
// just after its opening `${` (i.e. src[start] is the first byte of the
// expression), balancing nested `{`/`}` (a map literal like `${{"a": 1}}`)
// and skipping over nested quoted substrings (so a `}` or a differently
// quoted string inside, e.g. `${a + "b}c"}`, doesn't end the template
// early) exactly as internal/expr's own value-template splitter does. It
// returns the inner text (unmodified) and the index just past the closing
// `}`, or an error if `${` never closes.
func scanTemplateBody(src string, start int) (string, int, error) {
	n := len(src)
	j := start
	depth := 1
	var quote byte
	for j < n {
		c := src[j]
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
				return src[start : j-1], j, nil
			}
		default:
			j++
		}
	}
	return "", 0, errs.New(errs.Expr, "unterminated `${` in expression").WithDetail("expr", src)
}
