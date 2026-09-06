package expr

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

// Compiled is a structured assertion compiled down to a single execution
// path: CEL (evaluated as a bool) or a schema-validator reference.
type Compiled struct {
	Expr    string
	Kind    string // "cel" | "schema"
	Message string
}

// CompileAssertion compiles a domain.Assertion into CEL (or a schema
// reference) per PLAN §8. Exactly one of {Expr, Status, LatencyMs, Schema,
// Path} must be set; anything else is rejected as errs.FlowInvalid.
func CompileAssertion(a domain.Assertion) (Compiled, error) {
	set := 0
	if a.Expr != "" {
		set++
	}
	if a.Status != nil {
		set++
	}
	if a.LatencyMs != nil {
		set++
	}
	if a.Schema != "" {
		set++
	}
	if a.Path != "" {
		set++
	}
	if set == 0 {
		return Compiled{}, errs.New(errs.FlowInvalid,
			"empty assertion: specify one of expr, status, latency_ms, schema, or path")
	}
	if set > 1 {
		return Compiled{}, errs.New(errs.FlowInvalid,
			"ambiguous assertion: specify exactly one of expr, status, latency_ms, schema, path")
	}

	switch {
	case a.Expr != "":
		return Compiled{Expr: a.Expr, Kind: "cel", Message: a.Message}, nil
	case a.Status != nil:
		return Compiled{Expr: fmt.Sprintf("status == %d", *a.Status), Kind: "cel", Message: a.Message}, nil
	case a.LatencyMs != nil:
		celExpr, err := compileRange(a.LatencyMs)
		if err != nil {
			return Compiled{}, err
		}
		return Compiled{Expr: celExpr, Kind: "cel", Message: a.Message}, nil
	case a.Schema != "":
		return Compiled{Expr: "schema:" + a.Schema, Kind: "schema", Message: a.Message}, nil
	default: // a.Path != ""
		celExpr, err := compilePathAssertion(a)
		if err != nil {
			return Compiled{}, err
		}
		return Compiled{Expr: celExpr, Kind: "cel", Message: a.Message}, nil
	}
}

func compileRange(r *domain.Range) (string, error) {
	var parts []string
	if r.Lt != nil {
		parts = append(parts, "latency_ms < "+floatLit(*r.Lt))
	}
	if r.Lte != nil {
		parts = append(parts, "latency_ms <= "+floatLit(*r.Lte))
	}
	if r.Gt != nil {
		parts = append(parts, "latency_ms > "+floatLit(*r.Gt))
	}
	if r.Gte != nil {
		parts = append(parts, "latency_ms >= "+floatLit(*r.Gte))
	}
	if len(parts) == 0 {
		return "", errs.New(errs.FlowInvalid, "latency_ms assertion needs at least one of lt, lte, gt, gte")
	}
	return strings.Join(parts, " && "), nil
}

func floatLit(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

func compilePathAssertion(a domain.Assertion) (string, error) {
	ops := 0
	if a.Eq != nil {
		ops++
	}
	if a.Neq != nil {
		ops++
	}
	if a.Exists != nil {
		ops++
	}
	if a.Matches != "" {
		ops++
	}
	if a.Contains != nil {
		ops++
	}
	if ops == 0 {
		return "", errs.New(errs.FlowInvalid,
			"assertion for path `%s` needs one of eq, neq, exists, matches, contains", a.Path)
	}
	if ops > 1 {
		return "", errs.New(errs.FlowInvalid,
			"assertion for path `%s` is ambiguous: specify exactly one of eq, neq, exists, matches, contains", a.Path)
	}

	switch {
	case a.Eq != nil:
		lit, err := celLiteral(a.Eq)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s == %s", a.Path, lit), nil
	case a.Neq != nil:
		lit, err := celLiteral(a.Neq)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s != %s", a.Path, lit), nil
	case a.Exists != nil:
		return compileExists(a.Path, *a.Exists)
	case a.Matches != "":
		return fmt.Sprintf("%s.matches(%s)", a.Path, celStringLiteral(a.Matches)), nil
	default: // a.Contains != nil
		if str, ok := a.Contains.(string); ok {
			return fmt.Sprintf("%s.contains(%s)", a.Path, celStringLiteral(str)), nil
		}
		lit, err := celLiteral(a.Contains)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s in %s", lit, a.Path), nil
	}
}

var indexSuffixRe = regexp.MustCompile(`^(.*)\[(\d+)\]$`)

func compileExists(path string, want bool) (string, error) {
	if m := indexSuffixRe.FindStringSubmatch(path); m != nil {
		container := m[1]
		if want {
			return fmt.Sprintf("size(%s) > %s", container, m[2]), nil
		}
		return fmt.Sprintf("size(%s) <= %s", container, m[2]), nil
	}
	if strings.Count(path, ".") < 1 {
		return "", errs.New(errs.FlowInvalid,
			"exists assertion path `%s` must have at least two segments (e.g. body.field)", path)
	}
	if want {
		return fmt.Sprintf("has(%s)", path), nil
	}
	return fmt.Sprintf("!has(%s)", path), nil
}
