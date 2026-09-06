// Package retrieval implements the agent context builder (PLAN.md §14): it
// composes the finished catalog, search, memory, and runs packages into one
// vendor-neutral ContextBundle for an external agent, plus a CatalogResolver
// that adapts internal/catalog to memory.Resolver so the engine wiring can
// hand memory.Store a real resolver backed by the catalog.
//
// Nothing in this package writes to the catalog, memory, or run stores: it
// is a pure, read-only composition layer.
//
// # Memory-aware operation discovery
//
// PLAN.md §25 and Sapien.md §23/§31/§48 describe a cross-service invariant
// (the qcomSkill example: a memory saved on rider-service.getRider's
// response field) that must resurface rider-service.getRider itself the next
// time an agent asks for something like "Create a QCOM allocation test" -
// even though lexical/BM25 search on that literal phrase does not surface
// getRider (it shares no strong token with the query), and the memory is
// only reachable in the first place via a concept/tag overlap on the
// *other* selected operation (allocation-service.allocate, whose service
// concepts include "qcom").
//
// Build closes that gap without a schema change: domain.OperationContext
// and domain.ContextBundle carry no "why was this added" field, so an
// operation pulled in by a memory is appended exactly like any other
// selected operation (Tier contract, a Score derived from the memory that
// named it - see expandFromMemories) and is otherwise indistinguishable in
// the bundle's shape. The provenance is not lost, though: the memory itself
// is always present alongside it in bundle.Memories (its Subject names the
// operation directly), so a consumer that wants the "why" reads it off the
// memory rather than off a synthetic annotation. Concretely, Build:
//
//  1. Resolves the initial operation list (search- or catalog-selected, or
//     both).
//  2. Runs the memories tier (PLAN.md §14 step 3) against that list.
//  3. Only when the operation list was intent-driven (req.Operations was
//     empty - an explicit list is respected as given and never expanded):
//     walks those memories in score order, and for the first
//     maxMemoryExpandedOperations whose subject names an operation not
//     already selected (Subject.Operation, or Subject.Error.Operation),
//     resolves and appends it. A memory naming an operation the catalog no
//     longer has is skipped, not an error.
//  4. Re-runs the docs/flows/runs tiers (PLAN.md §14 steps 2/4/5) against
//     the expanded operation list, so a doc section or flow that only
//     references the newly-added operation is now reachable.
//  5. Re-runs the memories tier exactly once more against the expanded
//     list (bounded to a single extra round) and merges the two rounds by
//     memory id, keeping the higher score - this is what lets a memory
//     attached only to the expanded operation (and invisible to round one,
//     which never had that operation's own subjects) join the bundle.
//
// Budget enforcement (PLAN.md §14 steps 6-7) is unaffected: expansion
// operations are appended after the initial selection, so the existing
// "reduce operations" tier - which always drops from the tail - drops them
// first under a tight budget, before it ever touches an operation the
// caller (or search, on the caller's behalf) asked for directly.
package retrieval
