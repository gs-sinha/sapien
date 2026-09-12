package openapi

import (
	"sync"

	"github.com/pb33f/libopenapi"
)

// libopenapi memoizes its work in process-global maps -- node hashes keyed
// by *yaml.Node, built schemas, JSON paths, plus sync.Pools holding node
// slices (libopenapi/cache.go lists all eight). Nothing ever evicts them,
// and because the keys are pointers into the document being parsed, each
// one pins that document's entire yaml tree and schema graph for the life
// of the process. The library's own comment on nodeHashCache says the
// pointers are "stable for the document lifetime", which is true; the cache
// simply outlives every document put into it.
//
// For a CLI that parses one spec and exits, that is free. For the daemon it
// is a leak with no ceiling: the watcher re-ingests a service on every
// change to its package, and every one of those parses is retained forever.
// Measured on the 1.2 MB contract in ~/.sapien/repos/…-sarathy, parsing the
// same document repeatedly and dropping every reference to it:
//
//	without ClearAllCaches   18.6, 35.6, 52.5, 69.4 … MB of live heap
//	with ClearAllCaches       1.7,  1.8,  1.8,  1.8 … MB
//
// +16.9 MB per parse, still live after two forced GCs. That is how a daemon
// reached a 1 GB heap of which 54% was retained libopenapi objects, with
// nothing in Sapien holding a reference to any of it -- Ingest returns
// domain types only, so once it has returned, none of this is reachable
// through anything we own.
//
// libopenapi exposes ClearAllCaches for exactly this ("call this between
// document lifecycles in long-running processes"), so Ingest calls it when
// it is done. The caches are pure memoization, so dropping them costs a
// little repeated work inside the next parse and nothing else -- but only
// the last ingest still running clears, so a batch of concurrent ingests
// (one per open workspace) keeps the benefit for the parses in flight
// rather than each one clearing out from under the others.
var parserCache struct {
	mu       sync.Mutex
	inFlight int
}

// retainParserCaches marks one ingest as using libopenapi's globals.
func retainParserCaches() {
	parserCache.mu.Lock()
	parserCache.inFlight++
	parserCache.mu.Unlock()
}

// releaseParserCaches drops libopenapi's globals once this is the last
// ingest still running. Pair it with retainParserCaches via defer, so an
// ingest that fails part-way through still releases what it allocated.
func releaseParserCaches() {
	parserCache.mu.Lock()
	defer parserCache.mu.Unlock()
	parserCache.inFlight--
	if parserCache.inFlight <= 0 {
		parserCache.inFlight = 0
		libopenapi.ClearAllCaches()
	}
}
