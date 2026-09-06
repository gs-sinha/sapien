// Package enginetest provides Fake, an in-memory implementation of
// engine.Engine used to test the HTTP server (internal/server) and the
// remote client (internal/engine/remote) without depending on the real
// engine.Local implementation, which is built elsewhere.
//
// Fake is not a behavioral model of engine.Local: search ranking, flow
// validation, and memory relevance are simplified to something deterministic
// and good enough for wire-format and round-trip tests. Errors use
// internal/errs codes so callers can exercise the same error paths a real
// engine would produce.
package enginetest

import (
	"sync"
	"time"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

// Call records one invocation made against a Fake, in the order it happened,
// so tests can assert what the server or Remote client actually sent down.
type Call struct {
	Method string
	Args   any
}

// Fake is an in-memory engine.Engine. All methods are safe for concurrent
// use. Construct one with New, optionally populate it with Seed, and read
// f.Calls to see what was invoked.
type Fake struct {
	mu sync.Mutex

	ws *domain.Workspace

	services     map[string]domain.Service      // by name/id
	operations   map[string]domain.Operation    // by operation id
	fields       map[string][]domain.Field      // by operation id
	schemas      map[string]domain.NamedSchema  // by "<service>.<name>"
	docs         map[string]domain.Doc          // by doc id
	flows        map[string]domain.Flow         // by flow id
	flowUpdated  map[string]time.Time           // by flow id, for FlowSummary.Updated
	runs         map[string]domain.Run          // by run id
	examples     map[string]domain.SavedExample // by example id
	runSeq       map[string]int                 // run id -> insertion order, for Purge
	seq          int
	memories     map[string]domain.Memory // by memory id
	environments map[string]domain.Environment
	defaultEnv   string
	secrets      map[string]string // name -> value; never returned by any API

	reindexedServices bool
	reindexedMemories bool

	events *bus

	// Calls records every call made against any sub-API, in order.
	Calls []Call

	// RunResult, when non-nil, is copied and returned (with ID/timestamps
	// refreshed) by RunFlow, RunFlowSource, and Call instead of the built-in
	// canned run. Tests can set this to script a specific outcome.
	RunResult *domain.Run
}

// New creates an empty Fake for workspace ws.
func New(ws *domain.Workspace) *Fake {
	return &Fake{
		ws:           ws,
		services:     map[string]domain.Service{},
		operations:   map[string]domain.Operation{},
		fields:       map[string][]domain.Field{},
		schemas:      map[string]domain.NamedSchema{},
		docs:         map[string]domain.Doc{},
		flows:        map[string]domain.Flow{},
		flowUpdated:  map[string]time.Time{},
		runs:         map[string]domain.Run{},
		examples:     map[string]domain.SavedExample{},
		runSeq:       map[string]int{},
		memories:     map[string]domain.Memory{},
		environments: map[string]domain.Environment{},
		secrets:      map[string]string{},
		events:       newBus(),
	}
}

// recordLocked appends a Call. The caller must already hold f.mu.
func (f *Fake) recordLocked(method string, args any) {
	f.Calls = append(f.Calls, Call{Method: method, Args: args})
}

// nextSeq returns an increasing counter under f.mu. The caller must already
// hold f.mu.
func (f *Fake) nextSeqLocked() int {
	f.seq++
	return f.seq
}

// Workspace returns the workspace this Fake was constructed with.
func (f *Fake) Workspace() *domain.Workspace { return f.ws }

// Close is a no-op; Fake holds no external resources.
func (f *Fake) Close() error {
	f.events.closeAll()
	return nil
}

func (f *Fake) Services() engine.ServiceAPI { return (*serviceAPI)(f) }
func (f *Fake) Catalog() engine.CatalogAPI  { return (*catalogAPI)(f) }
func (f *Fake) Search() engine.SearchAPI    { return (*searchAPI)(f) }
func (f *Fake) Flows() engine.FlowAPI       { return (*flowAPI)(f) }
func (f *Fake) Runner() engine.RunnerAPI    { return (*runnerAPI)(f) }
func (f *Fake) Runs() engine.RunAPI         { return (*runAPI)(f) }
func (f *Fake) Memories() engine.MemoryAPI  { return (*memoryAPI)(f) }
func (f *Fake) Examples() engine.ExampleAPI { return (*exampleAPI)(f) }
func (f *Fake) Context() engine.ContextAPI  { return (*contextAPI)(f) }
func (f *Fake) Envs() engine.EnvAPI         { return (*envAPI)(f) }
func (f *Fake) Events() engine.EventAPI     { return (*eventAPI)(f) }

// Publish sends ev to every current Events().Subscribe subscriber.
func (f *Fake) Publish(ev domain.Event) { f.events.publish(ev) }

var _ engine.Engine = (*Fake)(nil)

// serviceAPI, catalogAPI, ... are all views of *Fake: each is defined with
// the same underlying struct so a *Fake converts to any of them for free,
// giving every group direct access to Fake's fields without an extra
// indirection or a second lock.
type (
	serviceAPI Fake
	catalogAPI Fake
	searchAPI  Fake
	flowAPI    Fake
	runnerAPI  Fake
	runAPI     Fake
	memoryAPI  Fake
	exampleAPI Fake
	contextAPI Fake
	envAPI     Fake
	eventAPI   Fake
)

func (s *serviceAPI) f() *Fake { return (*Fake)(s) }
func (c *catalogAPI) f() *Fake { return (*Fake)(c) }
func (s *searchAPI) f() *Fake  { return (*Fake)(s) }
func (fl *flowAPI) f() *Fake   { return (*Fake)(fl) }
func (r *runnerAPI) f() *Fake  { return (*Fake)(r) }
func (r *runAPI) f() *Fake     { return (*Fake)(r) }
func (m *memoryAPI) f() *Fake  { return (*Fake)(m) }
func (x *exampleAPI) f() *Fake { return (*Fake)(x) }
func (c *contextAPI) f() *Fake { return (*Fake)(c) }
func (e *envAPI) f() *Fake     { return (*Fake)(e) }
func (e *eventAPI) f() *Fake   { return (*Fake)(e) }
