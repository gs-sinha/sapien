package expr

import (
	"sync"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/parser"

	"github.com/gs-sinha/sapien/internal/errs"
)

// Evaluator compiles and evaluates CEL expressions against a Scope. It
// caches compiled programs by expression string and is safe for concurrent
// use.
type Evaluator struct {
	mu    sync.RWMutex
	cache map[string]*compiledExpr
}

type compiledExpr struct {
	prog cel.Program
	ast  *cel.Ast
}

// New creates an Evaluator with an empty compilation cache.
func New() *Evaluator {
	return &Evaluator{cache: make(map[string]*compiledExpr)}
}

func (e *Evaluator) lookup(expr string) (*compiledExpr, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	c, ok := e.cache[expr]
	return c, ok
}

func (e *Evaluator) store(expr string, c *compiledExpr) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cache[expr] = c
}

// compile parses, checks, and plans expr, or returns it from cache.
// hasCurrent/hasIter only affect the "available roots" text of an
// unknown-variable error; successful compilations are cached independently
// of them, since the shared environment always declares every root.
func (e *Evaluator) compile(expr string, hasCurrent, hasIter bool) (*compiledExpr, error) {
	if c, ok := e.lookup(expr); ok {
		return c, nil
	}
	ast, iss := sharedEnv.Compile(expr)
	if iss != nil && len(iss.Errors()) > 0 {
		return nil, checkErr(expr, iss.Errors(), hasCurrent, hasIter)
	}
	prog, err := sharedEnv.Program(ast)
	if err != nil {
		return nil, errs.Wrap(errs.Expr, err, "compiling expression").WithDetail("expr", expr)
	}
	c := &compiledExpr{prog: prog, ast: ast}
	e.store(expr, c)
	return c, nil
}

// checkScopeRoots reports a friendly error when expr statically references a
// current-step-only root (status/headers/body/latency_ms/request/out) but
// scope has no Current step, or `iter` but scope has no Iter (PLAN §34f.8).
func checkScopeRoots(ast *cel.Ast, expr string, s Scope) error {
	if s.hasCurrent() && s.hasIter() {
		return nil
	}
	var refs []Ref
	visitRefs(ast.NativeRep().Expr(), &refs)
	for _, r := range refs {
		if currentOnlyRoots[r.Root] && !s.hasCurrent() {
			return unavailableRootErr(r.Root, expr)
		}
		if iterOnlyRoots[r.Root] && !s.hasIter() {
			return unavailableRootErr(r.Root, expr)
		}
	}
	return nil
}

// Eval compiles (or reuses a cached compilation of) expr and evaluates it
// against s.
func (e *Evaluator) Eval(expr string, s Scope) (any, error) {
	c, err := e.compile(expr, s.hasCurrent(), s.hasIter())
	if err != nil {
		return nil, err
	}
	if err := checkScopeRoots(c.ast, expr, s); err != nil {
		return nil, err
	}
	out, _, err := c.prog.Eval(s.activation())
	if err != nil {
		return nil, friendlyEvalErr(expr, err)
	}
	v, err := toGoValue(out)
	if err != nil {
		return nil, friendlyEvalErr(expr, err)
	}
	return v, nil
}

// EvalBool evaluates expr and requires the result to be a bool.
func (e *Evaluator) EvalBool(expr string, s Scope) (bool, error) {
	v, err := e.Eval(expr, s)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, errs.New(errs.Expr, "expression `%s` must evaluate to a bool, got %T", expr, v).
			WithDetail("expr", expr)
	}
	return b, nil
}

// comparisonOps are the binary comparison operators Describe understands.
var comparisonOps = map[string]bool{
	"_==_": true, "_!=_": true,
	"_<_": true, "_<=_": true, "_>_": true, "_>=_": true,
}

// Describe is a best-effort helper for reporting the "actual" value of a
// comparison assertion: for an expression of the form `X op Y`, it evaluates
// and returns X. ok is false when expr is not such a comparison, or when X
// fails to evaluate.
func Describe(expr string, s Scope) (actual any, ok bool) {
	ast, iss := sharedEnv.Parse(expr)
	if iss != nil && len(iss.Errors()) > 0 {
		return nil, false
	}
	native := ast.NativeRep()
	root := native.Expr()
	if root.Kind() != celast.CallKind {
		return nil, false
	}
	call := root.AsCall()
	if !comparisonOps[call.FunctionName()] || len(call.Args()) != 2 {
		return nil, false
	}
	left := call.Args()[0]
	txt, err := parser.Unparse(left, native.SourceInfo())
	if err != nil {
		return nil, false
	}
	ev := New()
	v, err := ev.Eval(txt, s)
	if err != nil {
		return nil, false
	}
	return v, true
}
