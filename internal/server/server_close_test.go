package server

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine/enginetest"
)

// TestClose_StopsRecentEventsSubscription checks that Server.Close's
// unsubscribe actually stops the background goroutine subscribeRecentEvents
// started: an event published after Close should not show up in a later
// snapshot.
func TestClose_StopsRecentEventsSubscription(t *testing.T) {
	ws := &domain.Workspace{Version: 1, Name: "logistics", Dir: t.TempDir()}
	fake := enginetest.New(ws)
	enginetest.Seed(fake)

	srv := New(Options{
		Engine:  fake,
		Token:   "test-token",
		Version: "1.0.0",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	fake.Publish(domain.Event{Type: domain.EventFlowChanged})
	require.Eventually(t, func() bool { return len(srv.recent.snapshot(10)) == 1 }, time.Second, 5*time.Millisecond)

	require.NoError(t, srv.Close())

	// Give the unsubscribe a moment to actually stop the goroutine before
	// publishing again.
	time.Sleep(20 * time.Millisecond)
	fake.Publish(domain.Event{Type: domain.EventFlowChanged})
	time.Sleep(20 * time.Millisecond)

	assert.Len(t, srv.recent.snapshot(10), 1, "no event published after Close should reach the buffer")
}

func TestClose_IsSafeWithoutASubscription(t *testing.T) {
	s := &Server{}
	assert.NoError(t, s.Close())
}
