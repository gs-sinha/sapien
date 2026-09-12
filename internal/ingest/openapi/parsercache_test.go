package openapi

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

// liveHeapBytes is the heap still reachable after collection. Two GCs
// because the first can leave finalizable objects alive for the second.
func liveHeapBytes() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// libopenapi memoizes in process-global maps keyed by pointers into the
// document being parsed, and nothing evicts them, so every parse used to
// pin its whole yaml tree and schema graph for the life of the process.
// Harmless in a CLI that parses once; in the daemon, which re-ingests a
// service on every change to its package, it is a leak with no ceiling --
// it put a 1 GB heap on a daemon that had been up for minutes, 54% of it
// retained libopenapi objects. Ingest releases them (parsercache.go).
//
// The assertion is on retention per parse rather than on any one cache,
// because only one of the eight is exported. On this fixture the difference
// is 252 KB/parse against 2 KB/parse -- the 25 KB budget below sits an order
// of magnitude clear of both, so this fails loudly if the release is lost
// without flaking on GC timing. A bigger contract leaks proportionally
// more: a 1.2 MB one measured at 16.9 MB per parse.
func TestIngest_DoesNotRetainParsedDocuments(t *testing.T) {
	opts := Options{ServiceID: "petstore", File: "api/openapi.yaml"}

	// One warm-up parse first: the first call through also populates
	// genuinely one-off state (regexp compilation, package-level tables)
	// that must not be counted as per-parse retention.
	mustIngestFile(t, "testdata/petstore-3.0.yaml", opts)

	const parses = 40
	before := liveHeapBytes()
	for i := 0; i < parses; i++ {
		mustIngestFile(t, "testdata/petstore-3.0.yaml", opts)
	}
	after := liveHeapBytes()

	perParse := (int64(after) - int64(before)) / parses
	t.Logf("live heap %.2f MB -> %.2f MB over %d parses (%d bytes/parse)",
		float64(before)/1e6, float64(after)/1e6, parses, perParse)
	assert.Less(t, perParse, int64(25<<10),
		"each parse is retaining memory after Ingest returned; libopenapi's global caches are not being released")
}
