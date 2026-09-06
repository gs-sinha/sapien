// Package events is Sapien's in-process event bus (PLAN.md §4). It fans out
// typed domain.Event values to any number of subscribers — HTTP's
// GET /v1/events WebSocket, `sapien … --watch`, the file watcher, and so on —
// without letting a slow subscriber block the publisher or any other
// subscriber.
package events

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/growsimplee/sapien/internal/domain"
)

// subscriberBuffer is the size of each subscriber's buffered channel.
const subscriberBuffer = 256

// Bus fans out published events to subscribers. The zero value is not usable;
// construct one with New. Safe for concurrent use.
type Bus struct {
	mu      sync.Mutex
	subs    map[*subscriber]struct{}
	dropped atomic.Int64 // cumulative, across every subscriber past and present
}

// subscriber is one Subscribe call's private state. mu guards closed so that
// send and unsubscribe can never race to use ch after it has been closed
// (which would otherwise panic): both hold mu around their decision to send
// to, or close, ch.
type subscriber struct {
	mu     sync.Mutex
	ch     chan domain.Event
	closed bool
}

// New returns a ready-to-use Bus.
func New() *Bus {
	return &Bus{subs: make(map[*subscriber]struct{})}
}

// Publish delivers event to every current subscriber. It never blocks: if a
// subscriber's buffer is full, Publish drops that subscriber's oldest buffered
// event to make room, so a slow subscriber can never stall the publisher or
// any other subscriber. Dropped events are counted; see Dropped.
func (b *Bus) Publish(event domain.Event) {
	b.mu.Lock()
	subs := make([]*subscriber, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.Unlock()

	for _, s := range subs {
		b.send(s, event)
	}
}

// send delivers event to s, dropping s's oldest buffered event (and counting
// the drop on b) if s's buffer is full, then retrying. It is a silent no-op
// if s has already unsubscribed (ch closed).
func (b *Bus) send(s *subscriber, event domain.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	for {
		select {
		case s.ch <- event:
			return
		default:
		}

		// Buffer full: drop the oldest queued event to make room, then retry.
		select {
		case <-s.ch:
			b.dropped.Add(1)
		default:
			// Someone else drained it between our two selects; just retry the send.
		}
	}
}

// Subscribe registers a new subscriber and returns a channel of events plus an
// unsubscribe function. The channel is closed (after the unsubscribe function
// runs, or ctx is done) — whichever happens first — and must not be sent to
// or closed by the caller. Calling the returned function more than once, or
// after ctx is already done, is safe and a no-op past the first call.
func (b *Bus) Subscribe(ctx context.Context) (<-chan domain.Event, func()) {
	s := &subscriber{ch: make(chan domain.Event, subscriberBuffer)}

	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, s)
			b.mu.Unlock()

			s.mu.Lock()
			s.closed = true
			close(s.ch)
			s.mu.Unlock()
		})
	}

	if ctx != nil {
		go func() {
			<-ctx.Done()
			unsubscribe()
		}()
	}

	return s.ch, unsubscribe
}

// Dropped returns how many buffered events have been dropped so far, summed
// across every subscriber (past and present). It exists for diagnostics and
// tests; there is no per-subscriber breakdown because subscribers are
// unidentified beyond their channel.
func (b *Bus) Dropped() int64 {
	return b.dropped.Load()
}

// Emit is a convenience for constructing and publishing an event in one call,
// stamping Time as time.Now().
func Emit(bus *Bus, typ domain.EventType, payload any) {
	bus.Publish(domain.Event{Type: typ, Time: time.Now(), Payload: payload})
}
