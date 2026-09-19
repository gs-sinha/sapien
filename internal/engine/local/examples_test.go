package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// engineExampleFromRun builds an engine.ExampleFromRun request, defaulting
// Source to nil (so FromRun falls back to {kind: user}).
func engineExampleFromRun(runID, stepID, id string) engine.ExampleFromRun {
	return engine.ExampleFromRun{RunID: runID, StepID: stepID, ID: id}
}

// openExamplesEngine opens a *Local against the standard three-service
// logistics fixture (order-service, allocation-service, rider-service),
// with a real (unskipped) staleness check so the catalog -- and
// l.serviceDirs, which example.Locator needs for service-scoped examples
// -- are fully populated.
func openExamplesEngine(t *testing.T) *Local {
	t.Helper()
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// diagnosticCodes extracts the "diagnostics" detail from an errs.Invalid
// error into a plain slice of codes, for easy assertions.
func diagnosticCodes(t *testing.T, err error) []string {
	t.Helper()
	e := errs.As(err)
	require.NotNil(t, e)
	diags, ok := e.Details["diagnostics"].([]domain.Diagnostic)
	require.True(t, ok, "expected Details[\"diagnostics\"] to be []domain.Diagnostic, got %T", e.Details["diagnostics"])
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = d.Code
	}
	return out
}

func validCreateOrderExample(id string) domain.SavedExample {
	return domain.SavedExample{
		ID:        id,
		Operation: "order-service.createOrder",
		Body: map[string]any{
			"customerId": "cust_1",
			"type":       "QCOM",
			"pickup":     map[string]any{"lat": 12.97, "lng": 77.59},
			"drop":       map[string]any{"lat": 12.93, "lng": 77.61},
		},
		Expect: &domain.ExampleExpect{Status: 201, Body: map[string]any{"orderId": "ord_0001"}},
		Tags:   []string{"qcom", "happy-path"},
	}
}

func TestExamples_Create_Valid_WorkspaceScope(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	created, err := l.Examples().Create(ctx, validCreateOrderExample("create-qcom-order"))
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, "create-qcom-order", created.ID)
	assert.Equal(t, domain.ExampleScopeWorkspace, created.Scope)
	assert.Equal(t, "order-service", created.Service)
	assert.Equal(t, 1, created.Version)
	// PLAN §7b: a new workspace-scope example lands in the local tier by
	// default, same as a new memory or flow, until it is moved to the
	// workspace tier.
	assert.Equal(t, domain.TierLocal, created.Tier)
	assert.Equal(t, filepath.Join(l.ws.Dir, domain.LocalDir, "examples"), filepath.Dir(created.Path))
	assert.FileExists(t, created.Path)

	got, err := l.Examples().Get(ctx, "create-qcom-order")
	require.NoError(t, err)
	assert.Equal(t, created.Operation, got.Operation)
}

func TestExamples_Create_Valid_ServiceScope(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	ex := validCreateOrderExample("create-qcom-order-svc")
	ex.Scope = domain.ExampleScopeService

	created, err := l.Examples().Create(ctx, ex)
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, domain.ExampleScopeService, created.Scope)

	svc, err := l.Services().Get(ctx, "order-service")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(svc.PackageDir, "examples"), filepath.Dir(created.Path))
	assert.FileExists(t, created.Path)
}

func TestExamples_Create_RejectsVerified(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	ex := validCreateOrderExample("cant-set-verified")
	ex.Verified = &domain.ExampleVerified{Env: "stage"}

	_, err := l.Examples().Create(ctx, ex)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, err.Error(), "verified is set only by FromRun")
}

func TestExamples_Create_UnknownOperation(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	ex := validCreateOrderExample("bad-op")
	ex.Operation = "order-service.createOrdr" // typo, close to createOrder

	_, err := l.Examples().Create(ctx, ex)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))

	e := errs.As(err)
	diags, ok := e.Details["diagnostics"].([]domain.Diagnostic)
	require.True(t, ok)
	require.NotEmpty(t, diags)
	assert.Equal(t, "UNKNOWN_OPERATION", diags[0].Code)
	assert.Equal(t, domain.SeverityError, diags[0].Severity)
	assert.Contains(t, diags[0].Message, "order-service.createOrdr")
	assert.NotEmpty(t, diags[0].Suggestions, "expected a fuzzy suggestion for a near-miss operation id")
	assert.Contains(t, diags[0].Suggestions, "order-service.createOrder")
}

func TestExamples_Create_UnknownOperation_EmptyOperation(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	ex := validCreateOrderExample("no-op")
	ex.Operation = ""

	_, err := l.Examples().Create(ctx, ex)
	require.Error(t, err)
	codes := diagnosticCodes(t, err)
	assert.Contains(t, codes, "UNKNOWN_OPERATION")
}

func TestExamples_Create_UnknownInputName(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	ex := domain.SavedExample{
		ID:        "get-order-bad-input",
		Operation: "order-service.getOrder",
		Input:     map[string]any{"orderId": "ord_1", "bogus": "x"},
	}

	_, err := l.Examples().Create(ctx, ex)
	require.Error(t, err)
	e := errs.As(err)
	diags, ok := e.Details["diagnostics"].([]domain.Diagnostic)
	require.True(t, ok)
	require.NotEmpty(t, diags)
	assert.Equal(t, "UNKNOWN_INPUT_NAME", diags[0].Code)
	assert.Contains(t, diags[0].Message, "bogus")
	assert.Contains(t, diags[0].Suggestions, "orderId (path)")
}

func TestExamples_Create_MissingBody(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	ex := domain.SavedExample{
		ID:        "create-order-no-body",
		Operation: "order-service.createOrder",
	}

	_, err := l.Examples().Create(ctx, ex)
	require.Error(t, err)
	codes := diagnosticCodes(t, err)
	assert.Contains(t, codes, "MISSING_BODY")
}

func TestExamples_Create_UnexpectedBodyIsOnlyAWarning(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	// getOrder takes no request body; a body is a warning, not an error, so
	// Create must still succeed (mirrors flow.Validator: warnings don't
	// block a write).
	ex := domain.SavedExample{
		ID:        "get-order-with-body",
		Operation: "order-service.getOrder",
		Input:     map[string]any{"orderId": "ord_1"},
		Body:      map[string]any{"unexpected": true},
	}

	created, err := l.Examples().Create(ctx, ex)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"unexpected": true}, created.Body)
}

func TestExamples_Create_SchemaRejectsBadID(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	ex := validCreateOrderExample("_leading-underscore-not-allowed")

	_, err := l.Examples().Create(ctx, ex)
	require.Error(t, err)
	codes := diagnosticCodes(t, err)
	assert.Contains(t, codes, "SCHEMA")
}

func TestExamples_Update_MovesFileOnScopeChange(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	created, err := l.Examples().Create(ctx, validCreateOrderExample("moves-scope"))
	require.NoError(t, err)
	workspacePath := created.Path

	updated := *created
	updated.Scope = domain.ExampleScopeService

	movedTo, err := l.Examples().Update(ctx, updated)
	require.NoError(t, err)
	assert.Equal(t, domain.ExampleScopeService, movedTo.Scope)
	assert.NoFileExists(t, workspacePath)
	assert.FileExists(t, movedTo.Path)
}

func TestExamples_Update_KeepsExistingOperationWhenUnset(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	created, err := l.Examples().Create(ctx, validCreateOrderExample("keep-operation"))
	require.NoError(t, err)

	partial := domain.SavedExample{
		ID:          created.ID,
		Description: "updated description",
		Body:        created.Body,
	}
	updated, err := l.Examples().Update(ctx, partial)
	require.NoError(t, err)
	assert.Equal(t, "order-service.createOrder", updated.Operation)
	assert.Equal(t, "updated description", updated.Description)
}

func TestExamples_Update_ValidatesLikeCreate(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	created, err := l.Examples().Create(ctx, validCreateOrderExample("update-validated"))
	require.NoError(t, err)

	bad := *created
	bad.Body = nil // createOrder requires a body

	_, err = l.Examples().Update(ctx, bad)
	require.Error(t, err)
	codes := diagnosticCodes(t, err)
	assert.Contains(t, codes, "MISSING_BODY")
}

func TestExamples_Delete(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	created, err := l.Examples().Create(ctx, validCreateOrderExample("to-delete"))
	require.NoError(t, err)

	require.NoError(t, l.Examples().Delete(ctx, created.ID))
	_, err = l.Examples().Get(ctx, created.ID)
	require.Error(t, err)
	assert.Equal(t, errs.ExampleNotFound, errs.CodeOf(err))
	assert.NoFileExists(t, created.Path)
}

func TestExamples_ListAndForOperations(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	_, err := l.Examples().Create(ctx, validCreateOrderExample("list-1"))
	require.NoError(t, err)
	_, err = l.Examples().Create(ctx, validCreateOrderExample("list-2"))
	require.NoError(t, err)

	list, err := l.Examples().List(ctx, domain.ExampleQuery{Operation: "order-service.createOrder"})
	require.NoError(t, err)
	assert.Len(t, list, 2)

	forOps, err := l.Examples().ForOperations(ctx, []string{"order-service.createOrder"}, 0)
	require.NoError(t, err)
	assert.Len(t, forOps, 2)
}

func TestExamples_Reindex(t *testing.T) {
	l := openExamplesEngine(t)
	ctx := context.Background()

	_, err := l.Examples().Create(ctx, validCreateOrderExample("reindex-me"))
	require.NoError(t, err)
	require.NoError(t, l.Examples().Reindex(ctx))

	got, err := l.Examples().Get(ctx, "reindex-me")
	require.NoError(t, err)
	assert.Equal(t, "reindex-me", got.ID)
}

func TestExamples_ReindexOnOpen_PicksUpFileDroppedIntoWorkspaceExamplesDir(t *testing.T) {
	ws, _ := setupWorkspace(t)

	// Write an example file directly to disk, as if it had been committed
	// separately, before ever opening the engine.
	dir := filepath.Join(ws.Dir, "examples")
	require.NoError(t, writeExampleFile(dir, "dropped-in", `version: 1
id: dropped-in
operation: order-service.createOrder
body: { customerId: cust_1, type: QCOM, pickup: { lat: 1, lng: 2 }, drop: { lat: 3, lng: 4 } }
`))

	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	got, err := l.Examples().Get(context.Background(), "dropped-in")
	require.NoError(t, err)
	assert.Equal(t, "order-service.createOrder", got.Operation)
}

// writeExampleFile writes an example YAML file directly, bypassing the
// engine and store entirely -- simulating a file that arrived on disk some
// other way (a service repo clone, a hand-authored file).
func writeExampleFile(dir, id, yaml string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, id+".example.yaml"), []byte(yaml), 0o644)
}

// --- run-step -> example (FromRun) -----------------------------------------

// seedRun inserts a run with the given steps directly via the runs store
// (bypassing the runner entirely), so FromRun tests can script an exact
// request/response without a real HTTP call.
func seedRun(t *testing.T, l *Local, env string, steps ...domain.StepResult) *domain.Run {
	t.Helper()
	ctx := context.Background()
	run := &domain.Run{Environment: env, Status: domain.RunPassed}
	require.NoError(t, l.runsStore.Create(ctx, run))
	for _, st := range steps {
		require.NoError(t, l.runsStore.AppendStep(ctx, run.ID, st))
	}
	got, err := l.runsStore.Get(ctx, run.ID)
	require.NoError(t, err)
	return got
}

func TestExamples_FromRun_RecoversPathAndQueryInput(t *testing.T) {
	l := openExamplesEngine(t)

	run := seedRun(t, l, "stage", domain.StepResult{
		StepID:    "fetch",
		Operation: "rider-service.listRiders",
		Status:    domain.StepPassed,
		Request: &domain.RequestRecord{
			Method: "GET",
			URL:    "http://localhost:8083/v1/riders?city=Bengaluru&online=true",
		},
		Response: &domain.ResponseRecord{Status: 200, Body: map[string]any{"riders": []any{}}},
	})

	created, err := l.Examples().FromRun(context.Background(), engineExampleFromRun(run.ID, "fetch", "listed-riders"))
	require.NoError(t, err)
	assert.Equal(t, "Bengaluru", created.Input["city"])
	assert.Equal(t, "true", created.Input["online"])
	require.NotNil(t, created.Verified)
	assert.Equal(t, "stage", created.Verified.Env)
	assert.Equal(t, run.ID, created.Verified.RunID)
	assert.Equal(t, "fetch", created.Verified.StepID)
	assert.WithinDuration(t, time.Now(), created.Verified.At, time.Minute)
	assert.Equal(t, "user", created.Verified.Source.Kind)
}

func TestExamples_FromRun_RecoversPathParam(t *testing.T) {
	l := openExamplesEngine(t)

	run := seedRun(t, l, "stage", domain.StepResult{
		StepID:    "fetch",
		Operation: "rider-service.getRider",
		Status:    domain.StepPassed,
		Request:   &domain.RequestRecord{Method: "GET", URL: "http://localhost:8083/v1/riders/R123"},
		Response:  &domain.ResponseRecord{Status: 200, Body: map[string]any{"riderId": "R123"}},
	})

	created, err := l.Examples().FromRun(context.Background(), engineExampleFromRun(run.ID, "fetch", "one-rider"))
	require.NoError(t, err)
	assert.Equal(t, "R123", created.Input["riderId"])
	assert.Equal(t, 200, created.Expect.Status)
}

func TestExamples_FromRun_FiltersAuthAndRedactedHeaders(t *testing.T) {
	l := openExamplesEngine(t)

	run := seedRun(t, l, "stage", domain.StepResult{
		StepID:    "create",
		Operation: "order-service.createOrder",
		Status:    domain.StepPassed,
		Request: &domain.RequestRecord{
			Method: "POST",
			URL:    "http://localhost:8081/v1/orders",
			Body:   map[string]any{"customerId": "cust_1"},
			Headers: map[string]string{
				"Authorization":       "Bearer abc123",
				"Cookie":              "session=abc",
				"Proxy-Authorization": "Basic xyz",
				"X-Api-Key":           "[REDACTED]",
				"X-Trace-Secret":      "***",
				"Content-Type":        "application/json",
				"User-Agent":          "sapien/dev",
				"Idempotency-Key":     "idem-1",
			},
		},
		Response: &domain.ResponseRecord{Status: 201, Body: map[string]any{"orderId": "ord_1"}},
	})

	created, err := l.Examples().FromRun(context.Background(), engineExampleFromRun(run.ID, "create", "created-order"))
	require.NoError(t, err)
	// Secrets, redacted values, and transport noise (Content-Type,
	// User-Agent) are dropped; an application header survives.
	assert.Equal(t, map[string]string{"Idempotency-Key": "idem-1"}, created.Headers)
	assert.Equal(t, map[string]any{"customerId": "cust_1"}, created.Body)
}

func TestExamples_FromRun_ParsesBodyRawJSON(t *testing.T) {
	l := openExamplesEngine(t)

	run := seedRun(t, l, "stage", domain.StepResult{
		StepID:    "create",
		Operation: "order-service.createOrder",
		Status:    domain.StepPassed,
		Request: &domain.RequestRecord{
			Method:  "POST",
			URL:     "http://localhost:8081/v1/orders",
			BodyRaw: `{"customerId":"cust_1"}`,
		},
		Response: &domain.ResponseRecord{Status: 201, BodyRaw: `{"orderId":"ord_1"}`},
	})

	created, err := l.Examples().FromRun(context.Background(), engineExampleFromRun(run.ID, "create", "raw-body-order"))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"customerId": "cust_1"}, created.Body)
	assert.Equal(t, map[string]any{"orderId": "ord_1"}, created.Expect.Body)
}

func TestExamples_FromRun_NonJSONBodyRawStaysAString(t *testing.T) {
	l := openExamplesEngine(t)

	run := seedRun(t, l, "stage", domain.StepResult{
		StepID:    "create",
		Operation: "order-service.createOrder",
		Status:    domain.StepPassed,
		Request:   &domain.RequestRecord{Method: "POST", URL: "http://localhost:8081/v1/orders", BodyRaw: "not json"},
		Response:  &domain.ResponseRecord{Status: 201, BodyRaw: "also not json"},
	})

	created, err := l.Examples().FromRun(context.Background(), engineExampleFromRun(run.ID, "create", "plain-body-order"))
	require.NoError(t, err)
	assert.Equal(t, "not json", created.Body)
	assert.Equal(t, "also not json", created.Expect.Body)
}

func TestExamples_FromRun_TruncatesLongStringBody(t *testing.T) {
	l := openExamplesEngine(t)

	long := strings.Repeat("x", exampleBodyTruncateLimit+100)
	run := seedRun(t, l, "stage", domain.StepResult{
		StepID:    "create",
		Operation: "order-service.createOrder",
		Status:    domain.StepPassed,
		Request:   &domain.RequestRecord{Method: "POST", URL: "http://localhost:8081/v1/orders", BodyRaw: "not json either"},
		Response:  &domain.ResponseRecord{Status: 201, BodyRaw: long},
	})

	created, err := l.Examples().FromRun(context.Background(), engineExampleFromRun(run.ID, "create", "truncated-order"))
	require.NoError(t, err)
	body, ok := created.Expect.Body.(string)
	require.True(t, ok)
	assert.Len(t, body, exampleBodyTruncateLimit+len("...[truncated]"))
	assert.True(t, strings.HasSuffix(body, "...[truncated]"))
}

func TestExamples_FromRun_DefaultsToOnlyStepWhenStepIDOmitted(t *testing.T) {
	l := openExamplesEngine(t)

	run := seedRun(t, l, "stage", domain.StepResult{
		StepID:    "create",
		Operation: "order-service.createOrder",
		Status:    domain.StepPassed,
		Request:   &domain.RequestRecord{Method: "POST", URL: "http://localhost:8081/v1/orders", Body: map[string]any{"customerId": "cust_1"}},
		Response:  &domain.ResponseRecord{Status: 201, Body: map[string]any{"orderId": "ord_1"}},
	})

	req := engineExampleFromRun(run.ID, "", "only-step-order")
	created, err := l.Examples().FromRun(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "create", created.Verified.StepID)
}

func TestExamples_FromRun_DefaultsToFirstStepWithRequest(t *testing.T) {
	l := openExamplesEngine(t)

	run := seedRun(t, l, "stage",
		domain.StepResult{StepID: "skipped", Operation: "order-service.createOrder", Status: domain.StepSkipped},
		domain.StepResult{
			StepID: "create", Operation: "order-service.createOrder", Status: domain.StepPassed,
			Request:  &domain.RequestRecord{Method: "POST", URL: "http://localhost:8081/v1/orders", Body: map[string]any{"customerId": "cust_1"}},
			Response: &domain.ResponseRecord{Status: 201, Body: map[string]any{"orderId": "ord_1"}},
		},
	)

	req := engineExampleFromRun(run.ID, "", "first-with-request")
	created, err := l.Examples().FromRun(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "create", created.Verified.StepID)
}

// TestExamples_FromRun_LoopBlockDefaultsToLatestIteration confirms StepID
// naming a loop block's nested step (PLAN §34f.8), which ran more than
// once, defaults to its LATEST execution when Iteration is nil, matching
// steps.<id>'s own "latest execution" rule.
func TestExamples_FromRun_LoopBlockDefaultsToLatestIteration(t *testing.T) {
	l := openExamplesEngine(t)
	iter := func(n int) *int { return &n }

	run := seedRun(t, l, "stage",
		domain.StepResult{
			StepID: "fetch", Iteration: iter(0), Parent: "each", Operation: "rider-service.getRider", Status: domain.StepPassed,
			Request: &domain.RequestRecord{Method: "GET", URL: "http://localhost:8083/v1/riders/R1"},
			Response: &domain.ResponseRecord{Status: 200, Body: map[string]any{"riderId": "R1"}},
		},
		domain.StepResult{
			StepID: "fetch", Iteration: iter(1), Parent: "each", Operation: "rider-service.getRider", Status: domain.StepPassed,
			Request: &domain.RequestRecord{Method: "GET", URL: "http://localhost:8083/v1/riders/R2"},
			Response: &domain.ResponseRecord{Status: 200, Body: map[string]any{"riderId": "R2"}},
		},
	)

	req := engineExampleFromRun(run.ID, "fetch", "latest-rider")
	created, err := l.Examples().FromRun(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "R2", created.Input["riderId"], "should default to the latest (iteration 1) execution")

	req2 := engineExampleFromRun(run.ID, "fetch", "first-rider")
	req2.Iteration = iter(0)
	created2, err := l.Examples().FromRun(context.Background(), req2)
	require.NoError(t, err)
	assert.Equal(t, "R1", created2.Input["riderId"], "an explicit iteration must select that execution, not the latest")
}

func TestExamples_FromRun_ErrorsWhenStepNotFound(t *testing.T) {
	l := openExamplesEngine(t)

	run := seedRun(t, l, "stage", domain.StepResult{
		StepID: "create", Operation: "order-service.createOrder", Status: domain.StepPassed,
		Request: &domain.RequestRecord{Method: "POST", URL: "http://localhost:8081/v1/orders", Body: map[string]any{}},
	})

	req := engineExampleFromRun(run.ID, "does-not-exist", "missing-step")
	_, err := l.Examples().FromRun(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestExamples_FromRun_ErrorsWhenRunNotFound(t *testing.T) {
	l := openExamplesEngine(t)

	req := engineExampleFromRun("run_does_not_exist", "", "missing-run")
	_, err := l.Examples().FromRun(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, errs.RunNotFound, errs.CodeOf(err))
}

func TestExamples_FromRun_ErrorsWhenStepHasNoRequest(t *testing.T) {
	l := openExamplesEngine(t)

	run := seedRun(t, l, "stage", domain.StepResult{
		StepID: "no-request", Operation: "order-service.createOrder", Status: domain.StepSkipped,
	})

	req := engineExampleFromRun(run.ID, "no-request", "no-request-example")
	_, err := l.Examples().FromRun(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestExamples_FromRun_UsesExplicitSource(t *testing.T) {
	l := openExamplesEngine(t)

	run := seedRun(t, l, "stage", domain.StepResult{
		StepID: "create", Operation: "order-service.createOrder", Status: domain.StepPassed,
		Request:  &domain.RequestRecord{Method: "POST", URL: "http://localhost:8081/v1/orders", Body: map[string]any{"customerId": "cust_1"}},
		Response: &domain.ResponseRecord{Status: 201, Body: map[string]any{"orderId": "ord_1"}},
	})

	req := engineExampleFromRun(run.ID, "create", "explicit-source")
	req.Source = &domain.MemorySource{Kind: "agent", Client: "claude-code"}
	created, err := l.Examples().FromRun(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "agent", created.Verified.Source.Kind)
	assert.Equal(t, "claude-code", created.Verified.Source.Client)
}
