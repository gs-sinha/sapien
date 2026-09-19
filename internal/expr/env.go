package expr

import (
	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/ext"
)

// allRoots is every root variable name, in the order used for error messages
// and Reference(). currentOnlyRoots is the subset only available inside a
// step's own assert/extract/until (i.e. when Scope.Current is set).
var (
	allRoots = []string{
		"inputs", "env", "steps",
		"status", "headers", "body", "latency_ms", "request", "out",
		"iter",
	}
	currentOnlyRoots = map[string]bool{
		"status":     true,
		"headers":    true,
		"body":       true,
		"latency_ms": true,
		"request":    true,
		"out":        true,
	}
	// iterOnlyRoots is `iter` (PLAN §34f.8, spelled `loop` there -- see
	// IterValue's doc for why this implementation uses `iter` instead):
	// available only inside a loop block's own nested-step expressions
	// (when Scope.Iter is set), never in the block's own foreach/when
	// (evaluated before any iteration) or outside a block at all.
	iterOnlyRoots = map[string]bool{"iter": true}
)

// availableRoots lists the roots usable in the current context, for error
// messages ("unknown variable `foo`; available: ...").
func availableRoots(hasCurrent, hasIter bool) []string {
	out := make([]string, 0, len(allRoots))
	for _, r := range allRoots {
		if currentOnlyRoots[r] && !hasCurrent {
			continue
		}
		if iterOnlyRoots[r] && !hasIter {
			continue
		}
		out = append(out, r)
	}
	return out
}

// sharedEnv is the single CEL environment used for every expression. All
// roots are always declared (regardless of whether a given Scope sets
// Current) so that caching compiled programs by expression string alone is
// sound; availability of the current-step-only roots is enforced at
// evaluation time by checkScopeRoots.
var sharedEnv = buildEnv()

func buildEnv() *cel.Env {
	env, err := cel.NewEnv(
		cel.Variable("inputs", cel.DynType),
		cel.Variable("env", cel.DynType),
		cel.Variable("steps", cel.DynType),
		cel.Variable("status", cel.IntType),
		cel.Variable("headers", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("body", cel.DynType),
		cel.Variable("latency_ms", cel.DoubleType),
		cel.Variable("request", cel.DynType),
		cel.Variable("out", cel.DynType),
		cel.Variable("iter", cel.DynType),
		cel.CrossTypeNumericComparisons(true),
		cel.OptionalTypes(),
		ext.Strings(),
	)
	if err != nil {
		panic("expr: building shared CEL environment: " + err.Error())
	}
	return env
}
