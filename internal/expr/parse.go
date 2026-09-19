package expr

import (
	"fmt"
	"strings"

	celast "cel.dev/cel-go/common/ast"

	"github.com/gs-sinha/sapien/internal/errs"
)

// Parse checks expr for syntax errors only; it does not require roots to be
// declared or resolvable (that is Eval/EvalBool's job, against a Scope).
// expr is expanded through ExpandTemplates first, so a `${...}` template
// anywhere in it (bare, or inside a string literal) is checked as the CEL
// it rewrites to; any error still quotes expr's own original text, not the
// expanded form.
func Parse(expr string) error {
	expanded, xerr := ExpandTemplates(expr)
	if xerr != nil {
		return xerr
	}
	_, iss := sharedEnv.Parse(expanded)
	if iss != nil && len(iss.Errors()) > 0 {
		return parseIssueErr(expr, iss.Errors()[0])
	}
	return nil
}

func parseIssueErr(expr string, first *celErr) error {
	e := errs.New(errs.Expr, "%s", first.Message).WithDetail("expr", expr)
	if first.Location != nil {
		e = e.WithDetail("position", first.Location.Column()+1)
	}
	return e
}

// Ref is one static reference to a root variable found in an expression, for
// use by the flow validator. For root "steps", the first path segment is
// split out into StepID (e.g. steps.create.body.orderId -> {Root: "steps",
// StepID: "create", Path: ["body", "orderId"]}); for every other root, Path
// is the full chain of field/index accesses (e.g. body.online -> {Root:
// "body", Path: ["online"]}).
type Ref struct {
	Root   string
	Path   []string
	StepID string
}

// Roots returns the set of static root references used in expr, expanded
// through ExpandTemplates first (see Parse) so a reference that was only
// visible inside a `${...}` template -- e.g. the `steps.a` in
// `body.x != "${steps.a.out.id}"` -- is found exactly like one written as
// bare CEL. It is best-effort: a syntactically invalid expression (or one
// ExpandTemplates itself rejects, e.g. an unterminated `${`) yields nil.
func Roots(expr string) []Ref {
	expanded, xerr := ExpandTemplates(expr)
	if xerr != nil {
		return nil
	}
	ast, iss := sharedEnv.Parse(expanded)
	if iss != nil && len(iss.Errors()) > 0 {
		return nil
	}
	var refs []Ref
	visitRefs(ast.NativeRep().Expr(), &refs)
	return dedupeRefs(refs)
}

func buildRef(root string, path []string) Ref {
	if root == "steps" && len(path) > 0 {
		return Ref{Root: "steps", StepID: path[0], Path: append([]string(nil), path[1:]...)}
	}
	return Ref{Root: root, Path: append([]string(nil), path...)}
}

// collectPath tries to read e as a maximal chain of ident.field.field[...]
// accesses rooted in a plain identifier. ok is false when e is not such a
// chain (e.g. it's rooted in a function call).
func collectPath(e celast.Expr) (root string, path []string, ok bool) {
	switch e.Kind() {
	case celast.IdentKind:
		return e.AsIdent(), nil, true
	case celast.SelectKind:
		sel := e.AsSelect()
		root, path, ok = collectPath(sel.Operand())
		if !ok {
			return "", nil, false
		}
		return root, append(path, sel.FieldName()), true
	case celast.CallKind:
		call := e.AsCall()
		if call.FunctionName() == "_[_]" && len(call.Args()) == 2 {
			root, path, ok = collectPath(call.Args()[0])
			if !ok {
				return "", nil, false
			}
			idx := call.Args()[1]
			if idx.Kind() == celast.LiteralKind {
				return root, append(path, fmt.Sprint(idx.AsLiteral().Value())), true
			}
		}
		return "", nil, false
	default:
		return "", nil, false
	}
}

// visitRefs walks the whole AST collecting root references. Once a node
// resolves as a maximal ident/select/index chain via collectPath, its
// children are not visited again (they were consumed by the chain).
func visitRefs(e celast.Expr, refs *[]Ref) {
	if e == nil {
		return
	}
	if root, path, ok := collectPath(e); ok {
		*refs = append(*refs, buildRef(root, path))
		return
	}
	switch e.Kind() {
	case celast.SelectKind:
		visitRefs(e.AsSelect().Operand(), refs)
	case celast.CallKind:
		call := e.AsCall()
		if call.IsMemberFunction() {
			visitRefs(call.Target(), refs)
		}
		for _, a := range call.Args() {
			visitRefs(a, refs)
		}
	case celast.ListKind:
		for _, el := range e.AsList().Elements() {
			visitRefs(el, refs)
		}
	case celast.MapKind:
		for _, en := range e.AsMap().Entries() {
			me := en.AsMapEntry()
			visitRefs(me.Key(), refs)
			visitRefs(me.Value(), refs)
		}
	case celast.StructKind:
		for _, f := range e.AsStruct().Fields() {
			visitRefs(f.AsStructField().Value(), refs)
		}
	case celast.ComprehensionKind:
		c := e.AsComprehension()
		visitRefs(c.IterRange(), refs)
		visitRefs(c.AccuInit(), refs)
		visitRefs(c.LoopCondition(), refs)
		visitRefs(c.LoopStep(), refs)
		visitRefs(c.Result(), refs)
	}
}

func dedupeRefs(refs []Ref) []Ref {
	seen := make(map[string]bool, len(refs))
	out := make([]Ref, 0, len(refs))
	for _, r := range refs {
		key := r.Root + "\x00" + r.StepID + "\x00" + strings.Join(r.Path, ".")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}
