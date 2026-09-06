package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

// recentEventsCap bounds the ring buffer behind GET /v1/events/recent
// (PLAN §34c): enough for a UI tab to reload and see what just happened,
// small enough that memory use never depends on how long the daemon (or
// how event-heavy the session) has been running.
const recentEventsCap = 500

// defaultRecentEventsLimit and maxRecentEventsLimit bound the `limit`
// query parameter GET /v1/events/recent accepts.
const (
	defaultRecentEventsLimit = 100
	maxRecentEventsLimit     = 500
)

// recentEvents is a fixed-capacity ring buffer of the most recently
// published engine events, oldest first. Safe for concurrent use: one
// background goroutine (started by subscribeRecentEvents) appends via
// record while any number of HTTP handlers call snapshot.
type recentEvents struct {
	mu  sync.Mutex
	buf []domain.Event // oldest first; trimmed to recentEventsCap
}

func newRecentEvents() *recentEvents {
	return &recentEvents{buf: make([]domain.Event, 0, recentEventsCap)}
}

// record appends ev, trimmed by trimEventForHistory of anything too large
// to keep in memory indefinitely, dropping the oldest entry once the
// buffer is at capacity.
func (b *recentEvents) record(ev domain.Event) {
	ev = trimEventForHistory(ev)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, ev)
	if over := len(b.buf) - recentEventsCap; over > 0 {
		b.buf = b.buf[over:]
	}
}

// snapshot returns the last limit events, oldest first. A limit beyond
// what's buffered, or negative, is clamped down to what's available; the
// [1, maxRecentEventsLimit] clamp on the query parameter itself is the
// caller's job (handleEventsRecent).
func (b *recentEvents) snapshot(limit int) []domain.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if limit > len(b.buf) {
		limit = len(b.buf)
	}
	if limit < 0 {
		limit = 0
	}
	out := make([]domain.Event, limit)
	copy(out, b.buf[len(b.buf)-limit:])
	return out
}

// subscribeRecentEvents subscribes buf to api for the life of ctx,
// feeding it from a background goroutine that exits once the subscribed
// channel closes (ctx cancelled, unsubscribe called, or -- for
// enginetest.Fake -- the engine itself closed). The returned func is that
// subscription's own unsubscribe, for Server.Close.
func subscribeRecentEvents(ctx context.Context, api engine.EventAPI, buf *recentEvents) func() {
	ch, unsubscribe := api.Subscribe(ctx)
	go func() {
		for ev := range ch {
			buf.record(ev)
		}
	}()
	return unsubscribe
}

// runEventSummary is the payload trimEventForHistory substitutes for a
// run.started/run.finished event's full domain.Run: identifying fields
// and the run's own summary, not its steps (each of which can carry a
// full request and response body). A viewer that needs the full run
// fetches GET /v1/runs/{id}.
type runEventSummary struct {
	ID          string            `json:"id"`
	FlowID      string            `json:"flow_id,omitempty"`
	Environment string            `json:"environment,omitempty"`
	Status      domain.RunStatus  `json:"status"`
	Started     time.Time         `json:"started"`
	Finished    time.Time         `json:"finished,omitempty"`
	DurationMs  int64             `json:"duration_ms,omitempty"`
	Trigger     string            `json:"trigger,omitempty"`
	Summary     domain.RunSummary `json:"summary"`
}

// trimEventForHistory returns ev with a full domain.Run payload (as
// EventRunStarted and EventRunFinished otherwise carry, Steps -- and
// therefore every step's request/response body -- included) replaced by
// runEventSummary. Every other event type's payload (a run.step's
// run_id/step_id/status map, a CatalogChange, a Memory, ...) is already
// small and passes through unchanged.
func trimEventForHistory(ev domain.Event) domain.Event {
	switch ev.Type {
	case domain.EventRunStarted, domain.EventRunFinished:
		if run := asRun(ev.Payload); run != nil {
			ev.Payload = runEventSummary{
				ID:          run.ID,
				FlowID:      run.FlowID,
				Environment: run.Environment,
				Status:      run.Status,
				Started:     run.Started,
				Finished:    run.Finished,
				DurationMs:  run.DurationMs,
				Trigger:     run.Trigger,
				Summary:     run.Summary,
			}
		}
	}
	return ev
}

// asRun recovers a *domain.Run from an event payload regardless of
// whether the emitter (currently always internal/runner) sent a pointer
// or a value.
func asRun(payload any) *domain.Run {
	switch v := payload.(type) {
	case *domain.Run:
		return v
	case domain.Run:
		return &v
	default:
		return nil
	}
}

// handleEventsRecent implements GET /v1/events/recent?limit=N: the last
// (default 100, max 500) buffered events, oldest first, so a UI reload
// shows what just happened without waiting on a fresh live WebSocket
// connection to accumulate history of its own.
func (s *Server) handleEventsRecent(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", defaultRecentEventsLimit)
	if limit <= 0 {
		limit = defaultRecentEventsLimit
	}
	if limit > maxRecentEventsLimit {
		limit = maxRecentEventsLimit
	}
	writeJSON(w, http.StatusOK, s.recent.snapshot(limit))
}
