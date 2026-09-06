// Package expr is Sapien's deterministic expression layer for flows (PLAN §8).
//
// It provides CEL evaluation over a fixed scope model (inputs, env, steps,
// and — inside a step's own assert/extract/until — status, headers, body,
// latency_ms, request, out), `${...}` template interpolation with secret
// substitution, compilation of structured assertions to CEL, and static
// reference extraction used by the flow validator.
//
// Everything in this package is pure and deterministic: no I/O, no clocks,
// no randomness. The only side-effecting hook is Scope.SecretResolver, which
// callers provide.
package expr
