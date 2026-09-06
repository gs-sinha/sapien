// Package runtime is Sapien's HTTP execution layer (PLAN §19) and redaction
// pipeline (PLAN §28). It assembles and sends HTTP requests built from a
// normalized Request, captures timings via httptrace, and scrubs secrets and
// configured header/JSON-path values before anything is persisted.
//
// The package has no knowledge of operations, flows, or environments: it
// operates on plain Request/Response values and a resolved domain.Transport,
// leaving auth resolution and secret lookup to callers.
package runtime
