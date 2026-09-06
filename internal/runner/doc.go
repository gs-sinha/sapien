// Package runner executes Sapien flows (PLAN.md §8, §9): deterministic,
// sequential step execution with polling, assertions, extraction,
// redaction, persistence, and events.
//
// A Runner drives one *domain.Flow at a time through its execution state
// machine (queued -> running -> passed | failed | errored | cancelled at the
// run level; pending -> resolving -> requesting -> (polling)* -> asserting ->
// passed | failed | errored | skipped | cancelled at the step level),
// producing a domain.Run with one domain.StepResult per step. It also
// exposes Call, a convenience for executing a single operation as a
// one-step flow, and ValidateSchema, a small validator for the normalized
// domain.Schema used by `schema: contract` assertions.
//
// The package depends only on already-finished engine layers (domain, errs,
// expr, runtime, env, runs, events, store) and never on internal/flow or
// internal/catalog, which are still in progress elsewhere.
package runner
