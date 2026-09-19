package runs_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/runs"
	"github.com/gs-sinha/sapien/internal/store"
)

func newStore(t *testing.T) (*runs.Store, *store.DB) {
	t.Helper()
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return runs.New(db), db
}

// dt returns a fixed, monotonic-clock-free UTC time offset by n seconds from
// a fixed epoch, so ordering/age tests are deterministic and time values
// round-trip exactly through RFC3339Nano.
func dt(n int) time.Time {
	return time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Second)
}

func fullStep(id string, idx int) domain.StepResult {
	return domain.StepResult{
		StepID:     id,
		Index:      idx,
		Operation:  "order-service.createOrder",
		Status:     domain.StepPassed,
		SkipReason: "", // a passed step never carries one; see TestAppendStep_SkipReasonRoundTrip
		Attempts:   2,
		Request: &domain.RequestRecord{
			Method:  "POST",
			URL:     "https://api.example.com/v1/orders",
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    map[string]any{"customerId": "cust_123", "type": "QCOM"},
			BodyRaw: `{"customerId":"cust_123","type":"QCOM"}`,
		},
		Response: &domain.ResponseRecord{
			Status:    201,
			Headers:   map[string]string{"X-Request-Id": "req_1"},
			Body:      map[string]any{"orderId": "ord_456", "total": float64(19.99)},
			BodyRaw:   `{"orderId":"ord_456","total":19.99}`,
			Truncated: false,
			Size:      42,
		},
		Timings: &domain.Timings{DNSMs: 1.5, ConnectMs: 2.5, TLSMs: 3.5, TTFBMs: 10.25, TotalMs: 20.75},
		Assertions: []domain.AssertionResult{
			{Expr: "status == 201", Passed: true, Actual: float64(201)},
			{Expr: "body.orderId != null", Passed: true, Actual: "ord_456"},
			{Expr: "body.missing == true", Passed: false, Actual: nil, Message: "field missing", Error: "no such field"},
		},
		Out: map[string]any{"orderId": "ord_456"},
		Error: &domain.ErrorInfo{
			Code:    "E_ASSERTION_FAILED",
			Message: "1 assertion failed",
			Details: map[string]any{"failed": float64(1)},
		},
		Started:  dt(1),
		Finished: dt(2),
	}
}

func assertStepEqual(t *testing.T, want, got domain.StepResult) {
	t.Helper()
	assert.Equal(t, want.StepID, got.StepID)
	assert.Equal(t, want.Index, got.Index)
	assert.Equal(t, want.Operation, got.Operation)
	assert.Equal(t, want.Status, got.Status)
	assert.Equal(t, want.SkipReason, got.SkipReason)
	assert.Equal(t, want.Attempts, got.Attempts)
	assert.Equal(t, want.Request, got.Request)
	assert.Equal(t, want.Response, got.Response)
	assert.Equal(t, want.Timings, got.Timings)
	assert.Equal(t, want.Assertions, got.Assertions)
	assert.Equal(t, want.Out, got.Out)
	assert.Equal(t, want.Error, got.Error)
	assert.True(t, want.Started.Equal(got.Started))
	assert.True(t, want.Finished.Equal(got.Finished))
}

func TestCreate_Defaults(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	run := &domain.Run{Environment: "local"}
	before := time.Now().UTC()
	require.NoError(t, s.Create(ctx, run))

	assert.NotEmpty(t, run.ID)
	assert.True(t, strings.HasPrefix(run.ID, "run_"))
	assert.Equal(t, domain.RunQueued, run.Status)
	assert.WithinDuration(t, before, run.Started, 2*time.Second)

	got, err := s.Get(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, run.ID, got.ID)
	assert.Equal(t, domain.RunQueued, got.Status)
	assert.True(t, got.Finished.IsZero())
	assert.Zero(t, got.DurationMs)
	assert.False(t, got.Pinned)
	assert.Nil(t, got.Error)
	assert.Equal(t, domain.RunSummary{}, got.Summary)
	assert.Empty(t, got.Steps)
	assert.Empty(t, got.Inputs)
	assert.Empty(t, got.OperationHashes)
}

func TestCreate_ExplicitIDAndStatusPreserved(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	run := &domain.Run{ID: "run_explicit", Status: domain.RunRunning, Started: dt(0), Environment: "staging"}
	require.NoError(t, s.Create(ctx, run))
	assert.Equal(t, "run_explicit", run.ID)

	got, err := s.Get(ctx, "run_explicit")
	require.NoError(t, err)
	assert.Equal(t, domain.RunRunning, got.Status)
	assert.True(t, dt(0).Equal(got.Started))
}

func TestGet_NotFound(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.Get(context.Background(), "run_missing")
	require.Error(t, err)
	assert.True(t, errs.Is(err, errs.RunNotFound))
}

func TestAppendStep_FullRoundTrip(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	run := &domain.Run{
		FlowID:          "order-allocation",
		FlowSnapshot:    "version: 1\nid: order-allocation\n",
		Environment:     "staging",
		Inputs:          map[string]any{"customerId": "cust_123", "count": float64(3)},
		Trigger:         "cli",
		OperationHashes: map[string]string{"order-service.createOrder": "abc123"},
		Status:          domain.RunRunning,
	}
	require.NoError(t, s.Create(ctx, run))

	step := fullStep("create", 0)
	require.NoError(t, s.AppendStep(ctx, run.ID, step))

	got, err := s.Get(ctx, run.ID)
	require.NoError(t, err)

	require.Len(t, got.Steps, 1)
	assertStepEqual(t, step, got.Steps[0])

	assert.Equal(t, run.FlowID, got.FlowID)
	assert.Equal(t, run.FlowSnapshot, got.FlowSnapshot)
	assert.Equal(t, run.Environment, got.Environment)
	assert.Equal(t, run.Inputs, got.Inputs)
	assert.Equal(t, run.Trigger, got.Trigger)
	assert.Equal(t, run.OperationHashes, got.OperationHashes)
}

// TestAppendStep_SkipReasonRoundTrip covers the "when" skip reason
// specifically (PLAN §34f.7): a skipped step's skip_reason survives a
// round trip through the run_steps table, which is NOT NULL DEFAULT ''
// (unlike the nullable *_json/operation columns), so an empty SkipReason
// must be written as the empty string, not SQL NULL.
func TestAppendStep_SkipReasonRoundTrip(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	run := &domain.Run{Environment: "local"}
	require.NoError(t, s.Create(ctx, run))

	require.NoError(t, s.AppendStep(ctx, run.ID, domain.StepResult{
		StepID: "release", Index: 0, Status: domain.StepSkipped, SkipReason: "when",
	}))
	require.NoError(t, s.AppendStep(ctx, run.ID, domain.StepResult{
		StepID: "cancelled-before-start", Index: 1, Status: domain.StepSkipped,
	}))

	got, err := s.Get(ctx, run.ID)
	require.NoError(t, err)
	require.Len(t, got.Steps, 2)
	assert.Equal(t, "when", got.Steps[0].SkipReason)
	assert.Empty(t, got.Steps[1].SkipReason, "a step skipped for a reason other than `when` carries no skip_reason")
}

func TestAppendStep_MultipleStepsOrderedByIndex(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	run := &domain.Run{Environment: "local"}
	require.NoError(t, s.Create(ctx, run))

	require.NoError(t, s.AppendStep(ctx, run.ID, domain.StepResult{StepID: "b", Index: 1, Status: domain.StepPassed}))
	require.NoError(t, s.AppendStep(ctx, run.ID, domain.StepResult{StepID: "a", Index: 0, Status: domain.StepPassed}))

	got, err := s.Get(ctx, run.ID)
	require.NoError(t, err)
	require.Len(t, got.Steps, 2)
	assert.Equal(t, "a", got.Steps[0].StepID)
	assert.Equal(t, "b", got.Steps[1].StepID)
}

func TestAppendStep_UpsertKeepsIndexAndSingleRow(t *testing.T) {
	s, db := newStore(t)
	ctx := context.Background()

	run := &domain.Run{Environment: "local"}
	require.NoError(t, s.Create(ctx, run))

	first := domain.StepResult{StepID: "create", Index: 0, Status: domain.StepRequesting}
	require.NoError(t, s.AppendStep(ctx, run.ID, first))

	second := domain.StepResult{StepID: "create", Index: 99, Status: domain.StepPassed, Attempts: 1}
	require.NoError(t, s.AppendStep(ctx, run.ID, second))

	var count int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM run_steps WHERE run_id = ?`, run.ID).Scan(&count))
	assert.Equal(t, 1, count)

	got, err := s.Get(ctx, run.ID)
	require.NoError(t, err)
	require.Len(t, got.Steps, 1)
	assert.Equal(t, 0, got.Steps[0].Index) // kept from first insert
	assert.Equal(t, domain.StepPassed, got.Steps[0].Status)
	assert.Equal(t, 1, got.Steps[0].Attempts)
}

func TestAppendStep_UnknownRun(t *testing.T) {
	s, _ := newStore(t)
	err := s.AppendStep(context.Background(), "run_missing", domain.StepResult{StepID: "x"})
	require.Error(t, err)
	assert.True(t, errs.Is(err, errs.RunNotFound))
}

func TestUpdate_RoundTrip(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	run := &domain.Run{Environment: "local", Status: domain.RunRunning}
	require.NoError(t, s.Create(ctx, run))

	run.Status = domain.RunFailed
	run.Finished = dt(10)
	run.DurationMs = 4321
	run.Summary = domain.RunSummary{StepsTotal: 2, StepsPassed: 1, StepsFailed: 1, Assertions: 3, AssertionsFailed: 1}
	run.Error = &domain.ErrorInfo{Code: "E_ASSERTION_FAILED", Message: "boom", Details: map[string]any{"step": "allocate"}}
	run.Pinned = true
	require.NoError(t, s.Update(ctx, run))

	got, err := s.Get(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunFailed, got.Status)
	assert.True(t, run.Finished.Equal(got.Finished))
	assert.Equal(t, int64(4321), got.DurationMs)
	assert.Equal(t, run.Summary, got.Summary)
	assert.Equal(t, run.Error, got.Error)
	assert.True(t, got.Pinned)
	assert.Equal(t, "local", got.Environment) // untouched by Update
}

func TestUpdate_NotFound(t *testing.T) {
	s, _ := newStore(t)
	err := s.Update(context.Background(), &domain.Run{ID: "run_missing", Status: domain.RunPassed})
	require.Error(t, err)
	assert.True(t, errs.Is(err, errs.RunNotFound))
}

func TestList_OrderingAndFilters(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	mk := func(flowID string, status domain.RunStatus, started time.Time, op string) string {
		r := &domain.Run{FlowID: flowID, Environment: "local", Status: status, Started: started}
		require.NoError(t, s.Create(ctx, r))
		if op != "" {
			require.NoError(t, s.AppendStep(ctx, r.ID, domain.StepResult{StepID: "s1", Index: 0, Operation: op, Status: domain.StepPassed}))
		}
		return r.ID
	}

	idOld := mk("flowA", domain.RunPassed, dt(0), "svc.opA")
	idMid := mk("flowA", domain.RunFailed, dt(100), "svc.opB")
	idNew := mk("flowB", domain.RunPassed, dt(200), "svc.opA")

	all, err := s.List(ctx, domain.RunFilter{})
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, []string{idNew, idMid, idOld}, []string{all[0].ID, all[1].ID, all[2].ID})
	for _, r := range all {
		assert.Nil(t, r.Steps)
	}

	byFlow, err := s.List(ctx, domain.RunFilter{FlowID: "flowA"})
	require.NoError(t, err)
	assert.Len(t, byFlow, 2)

	byStatus, err := s.List(ctx, domain.RunFilter{Status: domain.RunFailed})
	require.NoError(t, err)
	require.Len(t, byStatus, 1)
	assert.Equal(t, idMid, byStatus[0].ID)

	byOp, err := s.List(ctx, domain.RunFilter{Operation: "svc.opA"})
	require.NoError(t, err)
	assert.Len(t, byOp, 2)

	limited, err := s.List(ctx, domain.RunFilter{Limit: 1})
	require.NoError(t, err)
	require.Len(t, limited, 1)
	assert.Equal(t, idNew, limited[0].ID)

	paged, err := s.List(ctx, domain.RunFilter{Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Len(t, paged, 1)
	assert.Equal(t, idMid, paged[0].ID)
}

func TestList_DefaultLimit(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	for i := 0; i < 55; i++ {
		r := &domain.Run{Environment: "local", Started: dt(i)}
		require.NoError(t, s.Create(ctx, r))
	}
	got, err := s.List(ctx, domain.RunFilter{})
	require.NoError(t, err)
	assert.Len(t, got, 50)
}

func TestList_SummaryPresentStepsOmitted(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	run := &domain.Run{Environment: "local"}
	require.NoError(t, s.Create(ctx, run))
	require.NoError(t, s.AppendStep(ctx, run.ID, domain.StepResult{StepID: "s1", Index: 0, Status: domain.StepPassed}))

	run.Status = domain.RunPassed
	run.Finished = dt(5)
	run.Summary = domain.RunSummary{StepsTotal: 1, StepsPassed: 1}
	require.NoError(t, s.Update(ctx, run))

	list, err := s.List(ctx, domain.RunFilter{})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, run.Summary, list[0].Summary)
	assert.Nil(t, list[0].Steps)
}

func TestPin(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	run := &domain.Run{Environment: "local"}
	require.NoError(t, s.Create(ctx, run))

	require.NoError(t, s.Pin(ctx, run.ID, true))
	got, err := s.Get(ctx, run.ID)
	require.NoError(t, err)
	assert.True(t, got.Pinned)

	require.NoError(t, s.Pin(ctx, run.ID, false))
	got, err = s.Get(ctx, run.ID)
	require.NoError(t, err)
	assert.False(t, got.Pinned)
}

func TestPin_NotFound(t *testing.T) {
	s, _ := newStore(t)
	err := s.Pin(context.Background(), "run_missing", true)
	require.Error(t, err)
	assert.True(t, errs.Is(err, errs.RunNotFound))
}

func TestDelete_CascadesToSteps(t *testing.T) {
	s, db := newStore(t)
	ctx := context.Background()
	run := &domain.Run{Environment: "local"}
	require.NoError(t, s.Create(ctx, run))
	require.NoError(t, s.AppendStep(ctx, run.ID, domain.StepResult{StepID: "s1", Index: 0, Status: domain.StepPassed}))

	require.NoError(t, s.Delete(ctx, run.ID))

	_, err := s.Get(ctx, run.ID)
	require.Error(t, err)
	assert.True(t, errs.Is(err, errs.RunNotFound))

	var count int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM run_steps WHERE run_id = ?`, run.ID).Scan(&count))
	assert.Equal(t, 0, count)
}

func TestDelete_NotFound(t *testing.T) {
	s, _ := newStore(t)
	err := s.Delete(context.Background(), "run_missing")
	require.Error(t, err)
	assert.True(t, errs.Is(err, errs.RunNotFound))
}

func TestPurge_ByKeep(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	var ids []string
	for i := 0; i < 5; i++ {
		r := &domain.Run{Environment: "local", Started: dt(i * 100)}
		require.NoError(t, s.Create(ctx, r))
		ids = append(ids, r.ID)
	}

	n, err := s.Purge(ctx, 2, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	remaining, err := s.List(ctx, domain.RunFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, remaining, 2)
	assert.Equal(t, ids[4], remaining[0].ID)
	assert.Equal(t, ids[3], remaining[1].ID)
}

func TestPurge_ByAge(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	old := &domain.Run{Environment: "local", Started: now.Add(-48 * time.Hour)}
	recent := &domain.Run{Environment: "local", Started: now.Add(-1 * time.Hour)}
	require.NoError(t, s.Create(ctx, old))
	require.NoError(t, s.Create(ctx, recent))

	n, err := s.Purge(ctx, 0, 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	_, err = s.Get(ctx, old.ID)
	assert.True(t, errs.Is(err, errs.RunNotFound))
	_, err = s.Get(ctx, recent.ID)
	require.NoError(t, err)
}

func TestPurge_PinnedExemptFromAge(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	old := &domain.Run{Environment: "local", Started: now.Add(-48 * time.Hour)}
	require.NoError(t, s.Create(ctx, old))
	require.NoError(t, s.Pin(ctx, old.ID, true))

	n, err := s.Purge(ctx, 0, 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	_, err = s.Get(ctx, old.ID)
	require.NoError(t, err)
}

func TestPurge_KeepIgnoresPinned(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	pinned := &domain.Run{Environment: "local", Started: dt(0)}
	require.NoError(t, s.Create(ctx, pinned))
	require.NoError(t, s.Pin(ctx, pinned.ID, true))

	for i := 1; i <= 3; i++ {
		r := &domain.Run{Environment: "local", Started: dt(i * 10)}
		require.NoError(t, s.Create(ctx, r))
	}

	n, err := s.Purge(ctx, 1, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, n) // 2 of the 3 unpinned beyond keep=1; pinned never counted

	remaining, err := s.List(ctx, domain.RunFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, remaining, 2) // pinned + 1 newest unpinned

	_, err = s.Get(ctx, pinned.ID)
	require.NoError(t, err)
}

func TestPurge_Disabled(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	r := &domain.Run{Environment: "local", Started: time.Now().UTC().Add(-1000 * time.Hour)}
	require.NoError(t, s.Create(ctx, r))

	n, err := s.Purge(ctx, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	_, err = s.Get(ctx, r.ID)
	require.NoError(t, err)
}

func TestStats(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	r1 := &domain.Run{Environment: "local", Status: domain.RunPassed, Started: dt(0)}
	r2 := &domain.Run{Environment: "local", Status: domain.RunFailed, Started: dt(100)}
	r3 := &domain.Run{Environment: "local", Status: domain.RunPassed, Started: dt(50)}
	require.NoError(t, s.Create(ctx, r1))
	require.NoError(t, s.Create(ctx, r2))
	require.NoError(t, s.Create(ctx, r3))

	st, err := s.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, st.Total)
	assert.Equal(t, 2, st.ByStatus[domain.RunPassed])
	assert.Equal(t, 1, st.ByStatus[domain.RunFailed])
	assert.True(t, dt(0).Equal(st.Oldest))
	assert.True(t, dt(100).Equal(st.Newest))
}

func TestStats_Empty(t *testing.T) {
	s, _ := newStore(t)
	st, err := s.Stats(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, st.Total)
	assert.True(t, st.Oldest.IsZero())
	assert.True(t, st.Newest.IsZero())
}

func TestSummarize(t *testing.T) {
	cases := []struct {
		name  string
		steps []domain.StepResult
		want  domain.RunSummary
	}{
		{"empty", nil, domain.RunSummary{}},
		{"all passed", []domain.StepResult{
			{Status: domain.StepPassed, Assertions: []domain.AssertionResult{{Passed: true}, {Passed: true}}},
			{Status: domain.StepPassed},
		}, domain.RunSummary{StepsTotal: 2, StepsPassed: 2, Assertions: 2}},
		{"mixed", []domain.StepResult{
			{Status: domain.StepPassed, Assertions: []domain.AssertionResult{{Passed: true}}},
			{Status: domain.StepFailed, Assertions: []domain.AssertionResult{{Passed: false}, {Passed: true}}},
			{Status: domain.StepErrored},
			{Status: domain.StepSkipped},
		}, domain.RunSummary{StepsTotal: 4, StepsPassed: 1, StepsFailed: 1, StepsErrored: 1, StepsSkipped: 1, Assertions: 3, AssertionsFailed: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, runs.Summarize(tc.steps))
		})
	}
}

func TestCreate_MarshalInputsError(t *testing.T) {
	s, _ := newStore(t)
	run := &domain.Run{Environment: "local", Inputs: map[string]any{"bad": make(chan int)}}
	err := s.Create(context.Background(), run)
	require.Error(t, err)
}

func TestUpdate_MarshalErrorInfoError(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	run := &domain.Run{Environment: "local"}
	require.NoError(t, s.Create(ctx, run))

	run.Error = &domain.ErrorInfo{Code: "E", Message: "m", Details: map[string]any{"bad": make(chan int)}}
	err := s.Update(ctx, run)
	require.Error(t, err)
}

func TestAppendStep_MarshalErrors(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	run := &domain.Run{Environment: "local"}
	require.NoError(t, s.Create(ctx, run))

	bad := make(chan int)
	cases := map[string]domain.StepResult{
		"request":    {StepID: "s1", Request: &domain.RequestRecord{Body: bad}},
		"response":   {StepID: "s2", Response: &domain.ResponseRecord{Body: bad}},
		"out":        {StepID: "s3", Out: map[string]any{"x": bad}},
		"assertions": {StepID: "s4", Assertions: []domain.AssertionResult{{Actual: bad}}},
		"error":      {StepID: "s5", Error: &domain.ErrorInfo{Details: map[string]any{"x": bad}}},
	}
	for name, step := range cases {
		err := s.AppendStep(ctx, run.ID, step)
		require.Errorf(t, err, "case %s", name)
	}
}

func TestGet_CorruptRunJSONColumns(t *testing.T) {
	s, db := newStore(t)
	ctx := context.Background()
	run := &domain.Run{Environment: "local", OperationHashes: map[string]string{"a.b": "h1"}}
	require.NoError(t, s.Create(ctx, run))
	run.Status = domain.RunFailed
	run.Error = &domain.ErrorInfo{Code: "E", Message: "m"}
	require.NoError(t, s.Update(ctx, run))

	for _, col := range []string{"inputs_json", "summary_json", "error_json", "operation_hashes_json"} {
		_, err := db.SQL().ExecContext(ctx, `UPDATE runs SET `+col+` = 'not-json' WHERE id = ?`, run.ID)
		require.NoError(t, err)

		_, err = s.Get(ctx, run.ID)
		require.Errorf(t, err, "corrupt column %s", col)

		_, err = db.SQL().ExecContext(ctx, `UPDATE runs SET `+col+` = '{}' WHERE id = ?`, run.ID)
		require.NoError(t, err)
	}
}

func TestGet_CorruptStepJSONColumns(t *testing.T) {
	s, db := newStore(t)
	ctx := context.Background()
	run := &domain.Run{Environment: "local"}
	require.NoError(t, s.Create(ctx, run))
	require.NoError(t, s.AppendStep(ctx, run.ID, domain.StepResult{
		StepID:     "s1",
		Request:    &domain.RequestRecord{Method: "GET"},
		Response:   &domain.ResponseRecord{Status: 200},
		Timings:    &domain.Timings{},
		Assertions: []domain.AssertionResult{{Expr: "true", Passed: true}},
		Out:        map[string]any{"a": "b"},
		Error:      &domain.ErrorInfo{Code: "E"},
	}))

	for _, col := range []string{"request_json", "response_json", "timings_json", "assertions_json", "out_json", "error_json"} {
		_, err := db.SQL().ExecContext(ctx, `UPDATE run_steps SET `+col+` = 'not-json' WHERE run_id = ?`, run.ID)
		require.NoError(t, err)

		_, err = s.Get(ctx, run.ID)
		require.Errorf(t, err, "corrupt column %s", col)

		_, err = db.SQL().ExecContext(ctx, `UPDATE run_steps SET `+col+` = NULL WHERE run_id = ?`, run.ID)
		require.NoError(t, err)
	}
}

func TestDeriveStatus(t *testing.T) {
	boom := errs.New(errs.HTTPTransport, "connection refused")
	cases := []struct {
		name      string
		steps     []domain.StepResult
		cancelled bool
		engineErr error
		want      domain.RunStatus
	}{
		{"cancelled wins over everything", []domain.StepResult{{Status: domain.StepPassed}}, true, boom, domain.RunCancelled},
		{"engine error wins over steps", []domain.StepResult{{Status: domain.StepPassed}}, false, boom, domain.RunErrored},
		{"any errored step", []domain.StepResult{{Status: domain.StepPassed}, {Status: domain.StepErrored}}, false, nil, domain.RunErrored},
		{"any failed step", []domain.StepResult{{Status: domain.StepPassed}, {Status: domain.StepFailed}, {Status: domain.StepSkipped}}, false, nil, domain.RunFailed},
		{"all passed", []domain.StepResult{{Status: domain.StepPassed}, {Status: domain.StepPassed}}, false, nil, domain.RunPassed},
		{"passed and skipped", []domain.StepResult{{Status: domain.StepPassed}, {Status: domain.StepSkipped}}, false, nil, domain.RunPassed},
		{"no steps", nil, false, nil, domain.RunErrored},
		{"all skipped, none passed", []domain.StepResult{{Status: domain.StepSkipped}}, false, nil, domain.RunErrored},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, runs.DeriveStatus(tc.steps, tc.cancelled, tc.engineErr))
		})
	}
}
