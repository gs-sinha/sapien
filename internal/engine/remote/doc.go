// Package remote implements engine.Engine as an HTTP client to a running
// sapien daemon (PLAN §4, §22). It is the counterpart of internal/server:
// every method here maps to exactly one route registered by that package,
// and every error the server returns is decoded back into an *errs.Error
// with the same Code, Message, Details, and Hint the underlying engine
// produced.
//
// Two things cannot cross the wire, both by construction rather than
// oversight:
//
//   - engine.RunOptions.Observer is a Go func value with no HTTP
//     representation. Remote drops it silently before encoding the request
//     (see runOptionsWire in runner.go). Callers that want to observe a run
//     as it happens must subscribe to Events().Subscribe instead: the local
//     engine and the daemon both publish run.started/run.step/run.finished
//     events for every run, whether started locally, over HTTP, or by another
//     client entirely.
//   - domain.MemoryQuery.Subjects has no query-string encoding on
//     GET /v1/memories and GET /v1/memories/search (PLAN §22 gives those
//     routes only q/scope/type/service/op/flow/limit/min_score). Remote's
//     MemoryAPI.List and MemoryAPI.Search therefore ignore Subjects; use
//     MemoryAPI.Relevant (POST /v1/memories/relevant), which does carry
//     Subjects in its JSON body, for subject-based retrieval over the wire.
//
// A related, pre-existing wire limitation worth knowing about (not
// introduced by this package): GET /v1/operations and GET /v1/docs serve
// both CatalogAPI.ListOperations/ListDocs and SearchAPI.Operations/Docs from
// the same route, branching server-side on whether "q" is present (PLAN
// §22's compact route sketch groups them deliberately). Calling
// SearchAPI.Operations or SearchAPI.Docs with an empty query is therefore
// indistinguishable, over the wire, from the corresponding CatalogAPI list
// call, and returns the list shape rather than scored search results.
// Real callers always pass a non-empty query to Search, so this only
// matters for tests exercising the boundary.
package remote
