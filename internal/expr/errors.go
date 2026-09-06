package expr

import (
	"regexp"
	"strings"

	"cel.dev/cel-go/cel"

	"github.com/gs-sinha/sapien/internal/errs"
)

// celErr is the per-issue error type cel-go's Issues carry (parse/check
// errors), each with a source Location when one is known.
type celErr = cel.Error

var undeclaredRefRe = regexp.MustCompile(`undeclared reference to '([^']+)'`)

// checkErr translates a compile-time (parse or check) failure from the
// shared env into a friendly errs.Expr, using hasCurrent to pick the right
// "available" root list for an unknown-variable message.
func checkErr(expr string, issues []*celErr, hasCurrent bool) error {
	first := issues[0]
	if m := undeclaredRefRe.FindStringSubmatch(first.Message); m != nil {
		e := errs.New(errs.Expr, "unknown variable `%s`; available: %s", m[1], strings.Join(availableRoots(hasCurrent), ", ")).
			WithDetail("expr", expr)
		if first.Location != nil {
			e = e.WithDetail("position", first.Location.Column()+1)
		}
		return e
	}
	e := errs.New(errs.Expr, "%s", first.Message).WithDetail("expr", expr)
	if first.Location != nil {
		e = e.WithDetail("position", first.Location.Column()+1)
	}
	return e
}

var (
	noSuchKeyRe  = regexp.MustCompile(`^no such key: (.+)$`)
	noSuchAttrRe = regexp.MustCompile(`^no such attribute\(s\): (.+)$`)
)

// friendlyEvalErr translates a runtime (evaluation) error into an errs.Expr,
// mapping CEL's generic "no such key: X" into a message naming the
// containing map when it can be derived from the source text, and mapping an
// unbound current-step-only root into the same guidance checkScopeRoots
// gives (a defensive fallback; checkScopeRoots should normally catch this
// before evaluation is attempted).
func friendlyEvalErr(expr string, err error) error {
	msg := err.Error()
	if m := noSuchKeyRe.FindStringSubmatch(msg); m != nil {
		key := m[1]
		if path := containerPath(expr, key); path != "" {
			return errs.New(errs.Expr, "no such key `%s` in %s", key, path).WithDetail("expr", expr)
		}
		return errs.New(errs.Expr, "no such key `%s`", key).WithDetail("expr", expr)
	}
	if m := noSuchAttrRe.FindStringSubmatch(msg); m != nil {
		name := strings.TrimSpace(strings.Split(m[1], ",")[0])
		if currentOnlyRoots[name] {
			return unavailableRootErr(name, expr)
		}
	}
	return errs.New(errs.Expr, "%s", msg).WithDetail("expr", expr)
}

// containerPath finds, in expr's source text, the map/select chain that
// precedes ".<key>" and returns it (e.g. for expr "steps.create.body.orderId"
// and key "orderId", returns "steps.create.body"). Empty when not found.
func containerPath(expr, key string) string {
	re := regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.]*)\.` + regexp.QuoteMeta(key) + `\b`)
	m := re.FindStringSubmatch(expr)
	if m == nil {
		return ""
	}
	return m[1]
}

func unavailableRootErr(root, expr string) error {
	return errs.New(errs.Expr,
		"`%s` is only available inside a step's assert/extract/until; use steps.<id>.%s", root, root).
		WithDetail("expr", expr)
}
