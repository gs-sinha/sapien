package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// TestRunHints_FailedStepWithDocMatch seeds a run (via Fake.RunResult,
// exactly as internal/diagnose's own tests build fixtures) whose one step
// failed with a 4xx response, crafted so its message is a literal
// substring of enginetest.Seed's one doc section ("Orders represent a
// customer purchase awaiting rider assignment."), and checks that
// GET /v1/runs/{id}/hints surfaces it.
func TestRunHints_FailedStepWithDocMatch(t *testing.T) {
	fake, ts := newTestServer(t, nil)

	fake.RunResult = &domain.Run{
		Status: domain.RunFailed,
		Steps: []domain.StepResult{{
			StepID:    "create",
			Operation: "order-service.createOrder", // seeded by enginetest.Seed
			Status:    domain.StepFailed,
			Response: &domain.ResponseRecord{
				Status: 409,
				Body:   map[string]any{"code": "ORDER_CONFLICT", "message": "orders represent a customer purchase"},
			},
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsFailed: 1},
	}

	run, err := fake.Runner().Call(context.Background(), engine.CallRequest{Operation: "order-service.createOrder"})
	require.NoError(t, err)

	resp := doReq(t, ts, http.MethodGet, "/v1/runs/"+run.ID+"/hints", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got runHintsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.NotEmpty(t, got.Hints, "expected at least one hint for the doc-matching failed step")

	found := false
	for _, h := range got.Hints {
		if h.Kind == "doc" && h.StepID == "create" {
			found = true
			assert.Equal(t, "order-service", h.Ref.Service)
		}
	}
	assert.True(t, found, "expected a doc hint for step %q, got %+v", "create", got.Hints)
}

// TestRunHints_PassedRunIsEmptyArray checks that a run with nothing to
// diagnose renders "hints": [] rather than null.
func TestRunHints_PassedRunIsEmptyArray(t *testing.T) {
	fake, ts := newTestServer(t, nil)

	runs, err := fake.Runs().List(context.Background(), domain.RunFilter{})
	require.NoError(t, err)
	require.NotEmpty(t, runs, "enginetest.Seed should have seeded a (passed) run")
	runID := runs[0].ID

	resp := doReq(t, ts, http.MethodGet, "/v1/runs/"+runID+"/hints", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"hints":[]`)
}

func TestRunHints_UnknownRunIs404(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/runs/run_does_not_exist/hints", reqOpts{token: "test-token"})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestRunHints_RequiresAuth(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/runs/whatever/hints", reqOpts{})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
