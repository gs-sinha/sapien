package server

import (
	"net/http"
	"net/http/pprof"
	"runtime"
	"runtime/debug"
)

// The daemon's runtime introspection surface: net/http/pprof plus a cheap
// always-on memory summary.
//
// Why this exists at all: a `sapien serve` process was observed growing to
// an 18 GB physical footprint in three hours while an agent bulk-wrote docs
// into a watched service package, and there was no way to ask the running
// process anything about its own heap -- no pprof, no counters -- so the
// only evidence available was `vmmap`, which cannot tell a leak (a lot of
// live objects) from churn (a lot of dead ones the OS has not reclaimed).
// GET /debug/memstats answers exactly that question in one request:
// heap_alloc says how much is live *now*, total_alloc says how much has
// ever been allocated, and a total_alloc climbing by gigabytes while
// heap_alloc stays flat is churn, not a leak.
//
// Why it is not in routeTable: routeTable is the daemon's public API
// contract -- it generates /v1/openapi.json, which agents and the browser
// UI read as the list of things they may call. These endpoints are an
// operator's debugging tool whose shape is Go's, not Sapien's (pprof's
// wire format is whatever the toolchain says it is, and MemStats gains
// fields with the Go version), so publishing them as API would promise a
// stability we cannot keep. They sit beside routeTable the same way
// GET /ui/session already does, and for the same reason.
//
// They are not, however, less guarded. Mounted on the same chi router,
// they inherit hostOriginMiddleware -- the loopback-only Host check of
// PLAN §28 -- from r.Use, and newRouter wraps them in authMiddleware
// explicitly, so a profile costs a caller exactly what any other route
// costs: a request from 127.0.0.1 carrying the daemon's bearer token.
// That matters more here than elsewhere: a heap profile of this process
// carries the contents of whatever it is holding.

// debugHandler builds the handler served under /debug: Go's pprof
// endpoints and this daemon's own memstats view.
//
// It routes with an inner http.ServeMux rather than chi routes because
// pprof.Index dispatches on the literal request path ("/debug/pprof/heap"
// -> the "heap" profile), which is what a ServeMux hands it unchanged;
// chi's Mount rewrites only its own routing path, so the two compose
// without either one having to know about the other.
func (s *Server) debugHandler() http.Handler {
	mux := http.NewServeMux()

	// pprof.Index serves the index page and every registered profile
	// (heap, allocs, goroutine, ...); the four below are the ones with
	// their own handlers rather than a runtime/pprof profile behind them.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	mux.HandleFunc("/debug/memstats", s.handleMemStats)

	return mux
}

// memStatsResponse is GET /debug/memstats: the handful of runtime numbers
// that answer "what is this daemon doing to memory right now", named in
// snake_case like the rest of the API's JSON.
//
// Byte counts are bytes. The three that matter most, in the order you read
// them: heap_alloc (live heap), total_alloc (every byte ever allocated,
// only ever increasing), and heap_released (returned to the OS). macOS
// keeps returned pages in a process's phys_footprint for a long time, so a
// daemon that looks enormous to Activity Monitor while heap_alloc is small
// and total_alloc is racing is allocating and freeing, not leaking.
type memStatsResponse struct {
	HeapAlloc     uint64  `json:"heap_alloc"`
	HeapSys       uint64  `json:"heap_sys"`
	HeapIdle      uint64  `json:"heap_idle"`
	HeapInuse     uint64  `json:"heap_inuse"`
	HeapReleased  uint64  `json:"heap_released"`
	HeapObjects   uint64  `json:"heap_objects"`
	Sys           uint64  `json:"sys"`
	TotalAlloc    uint64  `json:"total_alloc"`
	Mallocs       uint64  `json:"mallocs"`
	Frees         uint64  `json:"frees"`
	NumGC         uint32  `json:"num_gc"`
	NextGC        uint64  `json:"next_gc"`
	LastGCUnixNs  uint64  `json:"last_gc_unix_ns"`
	PauseTotalNs  uint64  `json:"pause_total_ns"`
	GCCPUFraction float64 `json:"gc_cpu_fraction"`
	NumGoroutine  int     `json:"num_goroutine"`
	GOMAXPROCS    int     `json:"gomaxprocs"`
	// MemoryLimit is the GOMEMLIMIT the daemon is running under, in bytes.
	// math.MaxInt64 means "no limit"; serve.go sets a real one by default
	// (see memoryLimit in internal/cli/serve.go).
	MemoryLimit int64 `json:"memory_limit"`
}

// handleMemStats implements GET /debug/memstats. runtime.ReadMemStats does
// stop the world, but only for microseconds and only for the caller who
// asked, which is the whole point: this is meant to be safe to poll every
// few seconds against a daemon in trouble.
func (s *Server) handleMemStats(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	writeJSON(w, http.StatusOK, memStatsResponse{
		HeapAlloc:     m.HeapAlloc,
		HeapSys:       m.HeapSys,
		HeapIdle:      m.HeapIdle,
		HeapInuse:     m.HeapInuse,
		HeapReleased:  m.HeapReleased,
		HeapObjects:   m.HeapObjects,
		Sys:           m.Sys,
		TotalAlloc:    m.TotalAlloc,
		Mallocs:       m.Mallocs,
		Frees:         m.Frees,
		NumGC:         m.NumGC,
		NextGC:        m.NextGC,
		LastGCUnixNs:  m.LastGC,
		PauseTotalNs:  m.PauseTotalNs,
		GCCPUFraction: m.GCCPUFraction,
		NumGoroutine:  runtime.NumGoroutine(),
		GOMAXPROCS:    runtime.GOMAXPROCS(0),
		MemoryLimit:   debug.SetMemoryLimit(-1), // -1 reads the limit without setting it
	})
}
