package expr

// StepValue is the recorded outcome of one flow step, exposed to expressions
// either as steps.<id> (from the Steps map) or, inside that step's own
// assert/extract/until, as the Current root fields directly (PLAN §8).
type StepValue struct {
	Request   map[string]any
	Status    int
	Headers   map[string]string
	Body      any
	LatencyMs float64
	Out       map[string]any

	// IsBlock marks a loop block's own StepValue (PLAN §34f.8): when true,
	// Count and Iterations are always rendered as CEL fields
	// (steps.<block>.count/.iterations), even when both are zero/empty (an
	// empty foreach list, or a repeat whose `while` was false up front) --
	// a plain call step's StepValue never has these keys at all.
	IsBlock bool
	// Count is the number of iterations the block actually ran.
	Count int
	// Iterations[i] is iteration i's nested step results, keyed by nested
	// step id (its LATEST execution as of that iteration, same "latest
	// wins" rule steps.<id> itself follows) -- steps.<block>.iterations.
	Iterations []map[string]StepValue
}

// IterValue is `iter` inside a loop block's nested-step expressions (PLAN
// §34f.8): Item is the current element for a foreach block (unset/nil for
// repeat), Index is the 0-based iteration number for either kind.
//
// PLAN §34f.8 names this root `loop` (loop.item/loop.index), but `loop` is
// a reserved CEL identifier -- cel-go's parser rejects it unconditionally
// ("reserved identifier: loop"), even though it is only ever declared as a
// variable, never used as CEL's own for/while syntax (CEL has neither).
// This is the one place this implementation deviates from the plan's exact
// wording: the root is `iter` (iter.item/iter.index) instead. See
// internal/flow.Reference's "Loop blocks" section and docs/flows.md for the
// user-facing spelling.
type IterValue struct {
	Item  any
	Index int
}

// Scope is the variable environment an expression is evaluated against.
type Scope struct {
	Inputs map[string]any
	Env    map[string]string
	Steps  map[string]StepValue

	// Current, when set, exposes status/headers/body/latency_ms/request/out
	// as roots — this is only true inside a step's own assert/extract/until.
	Current *StepValue

	// Iter, when set, exposes `iter.item`/`iter.index` — this is only true
	// inside a loop block's own nested-step expressions (PLAN §34f.8), never
	// in the block's own `foreach`/`when` (evaluated before any iteration).
	// See IterValue's doc for why this isn't named `loop`, as PLAN §34f.8
	// literally has it.
	Iter *IterValue

	// AllowSecrets permits `secret.NAME` inside Interpolate templates,
	// resolved through SecretResolver. Eval/EvalBool never allow secrets:
	// secret.* is not an expression root (PLAN §8).
	AllowSecrets   bool
	SecretResolver func(name string) (string, error)
}

// hasCurrent reports whether the current-step-only roots are available.
func (s Scope) hasCurrent() bool { return s.Current != nil }

// hasIter reports whether the `iter` root is available.
func (s Scope) hasIter() bool { return s.Iter != nil }

// activation builds the CEL activation map for this scope. Values are plain
// Go maps/slices/scalars; cel-go's default type adapter wraps them lazily.
func (s Scope) activation() map[string]any {
	vars := map[string]any{
		"inputs": normalizeJSON(orMap(s.Inputs)),
		"env":    orStringMap(s.Env),
		"steps":  stepsToNative(s.Steps),
	}
	if c := s.Current; c != nil {
		vars["status"] = int64(c.Status)
		vars["headers"] = orStringMap(c.Headers)
		vars["body"] = normalizeJSON(c.Body)
		vars["latency_ms"] = c.LatencyMs
		vars["request"] = normalizeJSON(orMap(c.Request))
		vars["out"] = normalizeJSON(orMap(c.Out))
	}
	if it := s.Iter; it != nil {
		vars["iter"] = map[string]any{
			"item":  normalizeJSON(it.Item),
			"index": int64(it.Index),
		}
	}
	return vars
}

func stepsToNative(steps map[string]StepValue) map[string]any {
	out := make(map[string]any, len(steps))
	for id, sv := range steps {
		out[id] = stepValueToNative(sv)
	}
	return out
}

func stepValueToNative(sv StepValue) map[string]any {
	out := map[string]any{
		"request":    normalizeJSON(orMap(sv.Request)),
		"status":     int64(sv.Status),
		"headers":    orStringMap(sv.Headers),
		"body":       normalizeJSON(sv.Body),
		"latency_ms": sv.LatencyMs,
		"out":        normalizeJSON(orMap(sv.Out)),
	}
	if sv.IsBlock {
		out["count"] = int64(sv.Count)
		iterations := make([]any, len(sv.Iterations))
		for i, m := range sv.Iterations {
			iterations[i] = stepsToNative(m)
		}
		out["iterations"] = iterations
	}
	return out
}

func orMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func orStringMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
