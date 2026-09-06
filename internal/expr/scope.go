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
}

// Scope is the variable environment an expression is evaluated against.
type Scope struct {
	Inputs map[string]any
	Env    map[string]string
	Steps  map[string]StepValue

	// Current, when set, exposes status/headers/body/latency_ms/request/out
	// as roots — this is only true inside a step's own assert/extract/until.
	Current *StepValue

	// AllowSecrets permits `secret.NAME` inside Interpolate templates,
	// resolved through SecretResolver. Eval/EvalBool never allow secrets:
	// secret.* is not an expression root (PLAN §8).
	AllowSecrets   bool
	SecretResolver func(name string) (string, error)
}

// hasCurrent reports whether the current-step-only roots are available.
func (s Scope) hasCurrent() bool { return s.Current != nil }

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
	return map[string]any{
		"request":    normalizeJSON(orMap(sv.Request)),
		"status":     int64(sv.Status),
		"headers":    orStringMap(sv.Headers),
		"body":       normalizeJSON(sv.Body),
		"latency_ms": sv.LatencyMs,
		"out":        normalizeJSON(orMap(sv.Out)),
	}
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
