package enginetest

import (
	"context"
	"sync"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

// subscriberBuffer is the channel buffer size for each subscriber, generous
// enough that Publish never blocks in tests that push a handful of events.
const subscriberBuffer = 64

// bus is a tiny in-memory pub/sub used to back EventAPI.Subscribe.
type bus struct {
	mu     sync.Mutex
	nextID int
	subs   map[int]chan domain.Event
	closed bool
}

func newBus() *bus {
	return &bus{subs: map[int]chan domain.Event{}}
}

func (b *bus) subscribe(ctx context.Context) (<-chan domain.Event, func()) {
	b.mu.Lock()
	ch := make(chan domain.Event, subscriberBuffer)
	if b.closed {
		close(ch)
		b.mu.Unlock()
		return ch, func() {}
	}
	id := b.nextID
	b.nextID++
	b.subs[id] = ch
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			if sub, ok := b.subs[id]; ok {
				delete(b.subs, id)
				close(sub)
			}
			b.mu.Unlock()
		})
	}

	if ctx != nil {
		go func() {
			<-ctx.Done()
			cancel()
		}()
	}

	return ch, cancel
}

func (b *bus) publish(ev domain.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// Subscriber isn't keeping up; drop rather than block Publish.
		}
	}
}

func (b *bus) closeAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, ch := range b.subs {
		delete(b.subs, id)
		close(ch)
	}
}

// Subscribe returns a channel of events and a cancel function. The channel
// closes when cancel is called or ctx is done.
func (e *eventAPI) Subscribe(ctx context.Context) (<-chan domain.Event, func()) {
	return e.f().events.subscribe(ctx)
}

var _ engine.EventAPI = (*eventAPI)(nil)
