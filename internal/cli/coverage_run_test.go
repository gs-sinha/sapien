package cli_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// --- run show --bodies: compactJSON rendering of request/response bodies ---

func TestRunShow_Bodies(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	runs, err := fake.Runs().List(context.Background(), domain.RunFilter{})
	require.NoError(t, err)
	require.NotEmpty(t, runs)
	runID := runs[0].ID

	stdout, stderr, code := run(t, "--workspace", dir, "run", "show", runID, "--bodies")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "REQUEST BODY")
	assert.Contains(t, stdout, "RESPONSE BODY")
	assert.Contains(t, stdout, `"customerId":"cust_1"`)
	assert.Contains(t, stdout, `"orderId":"order_1"`)
}

// --- run show --step: full human step detail (headers, mixed assertions,
// step error) ---

func TestRunShow_StepDetail_Human(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		FlowID:     "create-order-flow",
		Status:     domain.RunFailed,
		Started:    now,
		Finished:   now.Add(20 * time.Millisecond),
		DurationMs: 20,
		Steps: []domain.StepResult{{
			StepID:    "step-detail",
			Operation: "order-service.createOrder",
			Status:    domain.StepFailed,
			Request: &domain.RequestRecord{
				Method: "POST", URL: "http://localhost:4010/v1/orders",
				Headers: map[string]string{"Authorization": "Bearer x", "Content-Type": "application/json"},
				Body:    map[string]any{"customerId": "cust_1"},
			},
			Response: &domain.ResponseRecord{
				Status:  400,
				Headers: map[string]string{"Content-Type": "application/json"},
				Body:    map[string]any{"error": "bad request"},
			},
			Assertions: []domain.AssertionResult{
				{Expr: "status == 200", Passed: false},
				{Expr: "body.ok == true", Passed: true},
			},
			Error:    &domain.ErrorInfo{Code: "E_ASSERTION_FAILED", Message: "assertion failed: status == 200"},
			Started:  now,
			Finished: now.Add(20 * time.Millisecond),
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsFailed: 1, Assertions: 2, AssertionsFailed: 1},
	}

	_, _, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow")
	require.Equal(t, 1, code)

	runs, err := fake.Runs().List(context.Background(), domain.RunFilter{})
	require.NoError(t, err)
	var runID string
	for _, r := range runs {
		if r.FlowID == "create-order-flow" && r.Status == domain.RunFailed {
			runID = r.ID
		}
	}
	require.NotEmpty(t, runID)

	stdout, stderr, code := run(t, "--workspace", dir, "run", "show", runID, "--step", "step-detail")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "step step-detail (order-service.createOrder)  failed")
	assert.Contains(t, stdout, "request: POST http://localhost:4010/v1/orders")
	assert.Contains(t, stdout, "Authorization: Bearer x")
	assert.Contains(t, stdout, "response: 400")
	assert.Contains(t, stdout, "Content-Type: application/json")
	assert.Contains(t, stdout, "✗ status == 200")
	assert.Contains(t, stdout, "✓ body.ok == true")
	assert.Contains(t, stdout, "error: assertion failed: status == 200")
}

// --- run pin --unpin ---

func TestRunPin_Unpin(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	runs, err := fake.Runs().List(context.Background(), domain.RunFilter{})
	require.NoError(t, err)
	runID := runs[0].ID

	_, stderr, code := run(t, "--workspace", dir, "run", "pin", runID)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code := run(t, "--workspace", dir, "run", "pin", runID, "--unpin", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, false, got["pinned"])

	got2, err := fake.Runs().Get(context.Background(), runID)
	require.NoError(t, err)
	assert.False(t, got2.Pinned)

	stdout2, stderr, code := run(t, "--workspace", dir, "run", "pin", runID, "--unpin")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout2, "unpinned "+runID)
}

func TestRunPin_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "run", "pin", "run_nope", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_RUN_NOT_FOUND", got["code"])
}

func TestRunPurge_Human(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "run", "purge", "--keep", "0")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "removed")
}

// --- run list: filters ---

func TestRunList_Filters(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "run", "list", "--flow", "create-order-flow", "--status", "passed", "--limit", "1", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var runs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &runs))
	assert.Len(t, runs, 1)
}
