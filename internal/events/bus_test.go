package events_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/events"
)

func TestBus_FanOutToMultipleSubscribers(t *testing.T) {
	bus := events.New()
	ctx := context.Background()

	const n = 3
	chans := make([]<-chan domain.Event, n)
	for i := 0; i < n; i++ {
		ch, unsub := bus.Subscribe(ctx)
		chans[i] = ch
		t.Cleanup(unsub)
	}

	want := domain.Event{Type: domain.EventCatalogChanged, Payload: "hello"}
	bus.Publish(want)

	for i, ch := range chans {
		select {
		case got := <-ch:
			assert.Equalf(t, want.Type, got.Type, "subscriber %d", i)
			assert.Equalf(t, want.Payload, got.Payload, "subscriber %d", i)
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out waiting for event", i)
		}
	}
}

func TestBus_CancelClosesChannel(t *testing.T) {
	bus := events.New()
	ctx, cancel := context.WithCancel(context.Background())

	ch, _ := bus.Subscribe(ctx)
	cancel()

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "expected channel to be closed (no value)")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel to close after ctx cancel")
	}
}

func TestBus_UnsubscribeClosesChannel(t *testing.T) {
	bus := events.New()
	ch, unsubscribe := bus.Subscribe(context.Background())

	unsubscribe()

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "expected channel to be closed (no value)")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel to close after unsubscribe")
	}
}

func TestBus_UnsubscribeIsIdempotent(t *testing.T) {
	bus := events.New()
	_, unsubscribe := bus.Subscribe(context.Background())

	assert.NotPanics(t, func() {
		unsubscribe()
		unsubscribe() // double close must not panic
	})
}

func TestBus_PublishAfterUnsubscribeDoesNotPanicOrDeliver(t *testing.T) {
	bus := events.New()
	ch, unsubscribe := bus.Subscribe(context.Background())
	unsubscribe()

	assert.NotPanics(t, func() {
		bus.Publish(domain.Event{Type: domain.EventFlowChanged})
	})

	_, ok := <-ch
	assert.False(t, ok, "expected closed channel to yield no more values")
}

func TestBus_SlowSubscriberDoesNotBlockPublisherOrOthers(t *testing.T) {
	bus := events.New()
	ctx := context.Background()

	slow, unsubSlow := bus.Subscribe(ctx) // intentionally never drained during the flood
	defer unsubSlow()
	fast, unsubFast := bus.Subscribe(ctx)
	defer unsubFast()

	require.Equal(t, 256, cap(slow))

	// Drain "fast" concurrently so it never becomes a bottleneck either.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range fast {
		}
	}()

	const published = 1000
	publishDone := make(chan struct{})
	go func() {
		defer close(publishDone)
		for i := 0; i < published; i++ {
			bus.Publish(domain.Event{Type: domain.EventRunStep, Payload: i})
		}
	}()

	select {
	case <-publishDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked: a slow, undrained subscriber must not stall the publisher")
	}

	unsubFast()
	<-done

	assert.Positive(t, bus.Dropped(), "the undrained slow subscriber should have overflowed its 256-buffer")
}

func TestBus_DropAccounting(t *testing.T) {
	bus := events.New()
	ch, unsubscribe := bus.Subscribe(context.Background())
	defer unsubscribe()

	require.Zero(t, bus.Dropped())

	// Fill the 256-buffer, then push more without draining, forcing drops.
	const overflow = 50
	const bufferSize = 256
	for i := 0; i < bufferSize+overflow; i++ {
		bus.Publish(domain.Event{Type: domain.EventRunStep, Payload: i})
	}

	assert.EqualValues(t, overflow, bus.Dropped())

	// The buffer should hold the most recent bufferSize events (oldest ones
	// were dropped to make room), i.e. payloads overflow..bufferSize+overflow-1.
	first := <-ch
	assert.Equal(t, overflow, first.Payload, "oldest remaining payload should reflect the dropped prefix")
}

func TestBus_ConcurrentPublishAndSubscribe(t *testing.T) {
	bus := events.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	// Publishers.
	for p := 0; p < 4; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				bus.Publish(domain.Event{Type: domain.EventMemoryCreated})
			}
		}()
	}

	// Subscribers that subscribe, read a bit, and unsubscribe, concurrently
	// with publishers — this is the scenario that would panic on a send vs.
	// close race if send() and unsubscribe() didn't share a lock.
	for s := 0; s < 8; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, unsub := bus.Subscribe(ctx)
			for i := 0; i < 10; i++ {
				select {
				case <-ch:
				default:
				}
			}
			unsub()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out; possible deadlock or panic in concurrent publish/subscribe")
	}
}

func TestEmit_StampsTimeAndPublishes(t *testing.T) {
	bus := events.New()
	ch, unsubscribe := bus.Subscribe(context.Background())
	defer unsubscribe()

	before := time.Now()
	events.Emit(bus, domain.EventRunFinished, domain.CatalogChange{Service: "riders"})
	after := time.Now()

	select {
	case got := <-ch:
		assert.Equal(t, domain.EventRunFinished, got.Type)
		assert.WithinRange(t, got.Time, before, after)
		payload, ok := got.Payload.(domain.CatalogChange)
		require.True(t, ok, "payload should be a domain.CatalogChange")
		assert.Equal(t, "riders", payload.Service)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for emitted event")
	}
}
