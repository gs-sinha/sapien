package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

func fetchRecent(t *testing.T, ts *httptest.Server, query string) []domain.Event {
	t.Helper()
	resp := doReq(t, ts, http.MethodGet, "/v1/events/recent"+query, reqOpts{token: "test-token"})
	defer resp.Body.Close()
	var got []domain.Event
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	return got
}

func TestEventsRecent_DefaultLimitAndOrder(t *testing.T) {
	fake, ts := newTestServer(t, nil)

	for i := 0; i < 5; i++ {
		fake.Publish(domain.Event{Type: domain.EventFlowChanged, Time: time.Now(), Payload: map[string]int{"i": i}})
	}
	require.Eventually(t, func() bool {
		return len(fetchRecent(t, ts, "")) == 5
	}, 2*time.Second, 10*time.Millisecond)

	got := fetchRecent(t, ts, "")
	require.Len(t, got, 5)
	for i, ev := range got {
		payload, ok := ev.Payload.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, float64(i), payload["i"], "events should come back oldest first")
	}
}

func TestEventsRecent_LimitClampedToMax(t *testing.T) {
	fake, ts := newTestServer(t, nil)

	const n = 10
	for i := 0; i < n; i++ {
		fake.Publish(domain.Event{Type: domain.EventFlowChanged, Time: time.Now()})
	}
	require.Eventually(t, func() bool { return len(fetchRecent(t, ts, "")) == n }, 2*time.Second, 10*time.Millisecond)

	got := fetchRecent(t, ts, "?limit=2")
	assert.Len(t, got, 2)

	got = fetchRecent(t, ts, fmt.Sprintf("?limit=%d", maxRecentEventsLimit+1000))
	assert.Len(t, got, n) // fewer than maxRecentEventsLimit are buffered, so it's not clamped down to 500
}

// TestEventsRecent_BufferCapIsBounded exercises recentEvents directly
// rather than through the engine's pub/sub bus: enginetest's bus (like
// the real one) drops events under a fast, unthrottled publisher rather
// than block Publish, so pushing recentEventsCap+50 events through it in
// a tight loop cannot reliably deliver all of them for a capacity test --
// the ring buffer's own trim-to-capacity logic is what's under test here.
func TestEventsRecent_BufferCapIsBounded(t *testing.T) {
	buf := newRecentEvents()
	for i := 0; i < recentEventsCap+50; i++ {
		buf.record(domain.Event{Type: domain.EventFlowChanged, Payload: i})
	}

	got := buf.snapshot(maxRecentEventsLimit)
	require.Len(t, got, recentEventsCap)
	// The oldest 50 recorded (payloads 0..49) should have been dropped;
	// the buffer keeps only the most recent recentEventsCap.
	assert.Equal(t, 50, got[0].Payload)
	assert.Equal(t, recentEventsCap+49, got[len(got)-1].Payload)
}

// TestEventsRecent_TrimsFullRunPayload is the memory-budget guard: a
// run.started/run.finished event as internal/runner actually emits it
// carries a full *domain.Run, including every step's request/response
// body. The buffer must never hold (or serve) that -- only an id/status
// summary.
func TestEventsRecent_TrimsFullRunPayload(t *testing.T) {
	fake, ts := newTestServer(t, nil)

	bigBody := map[string]any{"blob": "this stands in for a large captured request/response body"}
	run := &domain.Run{
		ID:          "run_1",
		FlowID:      "flow_1",
		Environment: "local",
		Status:      domain.RunPassed,
		Started:     time.Now(),
		Finished:    time.Now(),
		DurationMs:  42,
		Trigger:     "cli",
		Summary:     domain.RunSummary{StepsTotal: 3, StepsPassed: 3},
		Steps: []domain.StepResult{{
			StepID:   "step1",
			Request:  &domain.RequestRecord{Method: "POST", URL: "http://example.test", Body: bigBody},
			Response: &domain.ResponseRecord{Status: 200, Body: bigBody},
		}},
	}
	fake.Publish(domain.Event{Type: domain.EventRunFinished, Time: time.Now(), Payload: run})

	require.Eventually(t, func() bool { return len(fetchRecent(t, ts, "")) == 1 }, 2*time.Second, 10*time.Millisecond)

	resp := doReq(t, ts, http.MethodGet, "/v1/events/recent", reqOpts{token: "test-token"})
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.NotContains(t, string(body), "blob", "trimmed event must not carry the run's step request/response bodies")

	var got []domain.Event
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got, 1)
	summary, ok := got[0].Payload.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "run_1", summary["id"])
	assert.Equal(t, "flow_1", summary["flow_id"])
	assert.Equal(t, string(domain.RunPassed), summary["status"])
	_, hasSteps := summary["steps"]
	assert.False(t, hasSteps, "trimmed event must not carry the run's steps at all")
}

func TestEventsRecent_RequiresAuth(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp := doReq(t, ts, http.MethodGet, "/v1/events/recent", reqOpts{})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// TestTrimEventForHistory_AcceptsRunByValue guards asRun's second branch:
// nothing in this repo emits EventRunFinished with a domain.Run value
// today (internal/runner always sends *domain.Run), but trimEventForHistory
// is not supposed to depend on that.
func TestTrimEventForHistory_AcceptsRunByValue(t *testing.T) {
	ev := domain.Event{
		Type: domain.EventRunStarted,
		Payload: domain.Run{
			ID:     "run_2",
			Status: domain.RunRunning,
		},
	}
	trimmed := trimEventForHistory(ev)
	summary, ok := trimmed.Payload.(runEventSummary)
	require.True(t, ok)
	assert.Equal(t, "run_2", summary.ID)
}

func TestTrimEventForHistory_UnrecognizedPayloadPassesThrough(t *testing.T) {
	ev := domain.Event{Type: domain.EventRunStarted, Payload: "not a run"}
	trimmed := trimEventForHistory(ev)
	assert.Equal(t, "not a run", trimmed.Payload)
}
