package cli_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/cli"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/engine/enginetest"
)

// setupFakeEngine creates a fresh on-disk workspace (via `sapien init`, so
// app.Workspace()/app.Engine() have something real to load) and wires
// cli.NewEngine to hand back a single Fake seeded with enginetest.Seed, so
// every command in a test sees the same fixed sample data. cli.NewEngine is
// restored when the test ends.
func setupFakeEngine(t *testing.T) (dir string, fake *enginetest.Fake) {
	t.Helper()
	dir = t.TempDir()
	_, stderr, code := run(t, "init", dir)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	fake = enginetest.New(&domain.Workspace{})
	enginetest.Seed(fake)

	orig := cli.NewEngine
	cli.NewEngine = func(*domain.Workspace) (engine.Engine, error) { return fake, nil }
	t.Cleanup(func() { cli.NewEngine = orig })

	return dir, fake
}

// --- call ---

func TestCall_Human(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder", "-p", "customerId=cust_1")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "200")
	assert.Contains(t, stdout, "ok")
}

func TestCall_JSON(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "passed", got["status"])
}

func TestCall_UnknownOperation(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "unknown.op", "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_OPERATION_NOT_FOUND", got["code"])
}

func TestCall_SaveAs(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder",
		"-p", "customerId=cust_1", "--save-as", "my-flow")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "my-flow")

	fl, err := fake.Flows().Get(context.Background(), "my-flow")
	require.NoError(t, err)
	require.Len(t, fl.Steps, 1)
	assert.Equal(t, "order-service.createOrder", fl.Steps[0].Call)
}

func TestCall_ProductionBlocked(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "--env", "production", "call", "order-service.createOrder", "--json")
	assert.Equal(t, 3, code)
	assert.Empty(t, stdout)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_PRODUCTION_BLOCKED", got["code"])
}

func TestCall_AllowProduction(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "--env", "production", "call", "order-service.createOrder", "--allow-production", "--json")
	assert.Equal(t, 0, code, "stderr: %s", stderr)
}

// --- flow ---

func TestFlowList(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "list", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var flows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &flows))
	assert.NotEmpty(t, flows)

	stdout, stderr, code = run(t, "--workspace", dir, "flow", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "create-order-flow")
}

func TestFlowShow(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "show", "create-order-flow")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "createOrder")

	stdout, stderr, code = run(t, "--workspace", dir, "flow", "show", "create-order-flow", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "create-order-flow", got["id"])
}

func TestFlowShow_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "show", "nope", "--json")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_FLOW_NOT_FOUND", got["code"])
}

func TestFlowValidate_ByID(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "validate", "create-order-flow")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "valid")
}

func TestFlowValidate_InvalidFile(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	badPath := filepath.Join(dir, "bad.flow.yaml")
	require.NoError(t, os.WriteFile(badPath, []byte(
		"version: 1\nid: bad-flow\nsteps:\n  - id: s1\n    call: no-such.operation\n"), 0o644))

	stdout, _, code := run(t, "--workspace", dir, "flow", "validate", badPath, "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, false, got["valid"])

	stdout2, _, code2 := run(t, "--workspace", dir, "flow", "validate", badPath)
	assert.Equal(t, 2, code2)
	assert.Contains(t, stdout2, "SEVERITY")
}

func TestFlowCreate(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	flowPath := filepath.Join(dir, "new-flow.flow.yaml")
	require.NoError(t, os.WriteFile(flowPath, []byte(
		"version: 1\nid: new-flow\nsteps:\n  - id: create\n    call: order-service.createOrder\n    body:\n      customerId: cust_2\n"), 0o644))

	stdout, stderr, code := run(t, "--workspace", dir, "flow", "create", flowPath)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	// The command prints a summary of what was saved (id and step count;
	// the real engine adds the destination path), never the document.
	assert.Contains(t, stdout, "saved flow new-flow")
	assert.Contains(t, stdout, "1 steps")

	_, err := fake.Flows().Get(context.Background(), "new-flow")
	require.NoError(t, err)
}

func TestFlowDelete(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "delete", "assign-rider-flow")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "assign-rider-flow")

	_, stderr, code = run(t, "--workspace", dir, "flow", "show", "assign-rider-flow", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_FLOW_NOT_FOUND", got["code"])
}

func TestFlowReference(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "reference", "flow")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "Flow DSL")
}

func TestFlowReference_Unknown(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "flow", "reference", "bogus", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
}

// --- flow run ---

func TestFlowRun_Passed(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "STEP")
	assert.Contains(t, stdout, "step1")
	assert.Contains(t, stdout, "passed")
}

func TestFlowRun_JSON(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "passed", got["status"])
}

func TestFlowRun_UnknownFlow(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "flow", "run", "no-such-flow", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_FLOW_NOT_FOUND", got["code"])
}

func TestFlowRun_BySource(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	flowPath := filepath.Join(dir, "adhoc.flow.yaml")
	require.NoError(t, os.WriteFile(flowPath, []byte(
		"version: 1\nid: adhoc\nsteps:\n  - id: create\n    call: order-service.createOrder\n    body:\n      customerId: cust_3\n"), 0o644))

	stdout, stderr, code := run(t, "--workspace", dir, "flow", "run", flowPath)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "step1")
}

func TestFlowRun_Watch(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow", "--watch")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "✓ step1")
}

func TestFlowRun_Failed(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		FlowID:     "create-order-flow",
		Status:     domain.RunFailed,
		Started:    now,
		Finished:   now.Add(10 * time.Millisecond),
		DurationMs: 10,
		Steps: []domain.StepResult{{
			StepID:    "create",
			Index:     0,
			Operation: "order-service.createOrder",
			Status:    domain.StepFailed,
			Response:  &domain.ResponseRecord{Status: 200},
			Assertions: []domain.AssertionResult{
				{Expr: "body.online == true", Passed: false, Message: "assertion failed"},
			},
			Started:  now,
			Finished: now.Add(5 * time.Millisecond),
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsFailed: 1, Assertions: 1, AssertionsFailed: 1},
	}

	stdout, _, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow")
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, "failed")
	assert.Contains(t, stdout, "body.online == true")
}

func TestFlowRun_Errored(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		FlowID:     "create-order-flow",
		Status:     domain.RunErrored,
		Started:    now,
		Finished:   now.Add(3 * time.Millisecond),
		DurationMs: 3,
		Error:      &domain.ErrorInfo{Code: "E_HTTP_TRANSPORT", Message: "connection refused"},
		Steps: []domain.StepResult{{
			StepID:   "create",
			Status:   domain.StepErrored,
			Error:    &domain.ErrorInfo{Code: "E_HTTP_TRANSPORT", Message: "connection refused"},
			Started:  now,
			Finished: now.Add(3 * time.Millisecond),
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsErrored: 1},
	}

	stdout, _, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow")
	assert.Equal(t, 2, code)
	assert.Contains(t, stdout, "errored")
	assert.Contains(t, stdout, "connection refused")
}

func TestFlowRun_ReportJUnit(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		FlowID:     "create-order-flow",
		Status:     domain.RunFailed,
		Started:    now,
		Finished:   now.Add(10 * time.Millisecond),
		DurationMs: 10,
		Steps: []domain.StepResult{
			{StepID: "create", Operation: "order-service.createOrder", Status: domain.StepPassed,
				Started: now, Finished: now.Add(4 * time.Millisecond)},
			{StepID: "assert-rider", Operation: "rider-service.getRider", Status: domain.StepFailed,
				Assertions: []domain.AssertionResult{{Expr: "body.online == true", Passed: false, Message: "rider offline"}},
				Started:    now.Add(4 * time.Millisecond), Finished: now.Add(10 * time.Millisecond)},
		},
		Summary: domain.RunSummary{StepsTotal: 2, StepsPassed: 1, StepsFailed: 1, Assertions: 1, AssertionsFailed: 1},
	}

	outFile := filepath.Join(dir, "report.xml")
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow", "--report", "junit", "--out", outFile)
	assert.Equal(t, 1, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, outFile)

	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	var suite struct {
		XMLName   xml.Name `xml:"testsuite"`
		Tests     int      `xml:"tests,attr"`
		Failures  int      `xml:"failures,attr"`
		Testcases []struct {
			Name    string `xml:"name,attr"`
			Failure *struct {
				Message string `xml:"message,attr"`
			} `xml:"failure"`
		} `xml:"testcase"`
	}
	require.NoError(t, xml.Unmarshal(data, &suite))
	assert.Equal(t, 2, suite.Tests)
	assert.Equal(t, 1, suite.Failures)
}

func TestFlowRun_ReportJSON(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow", "--report", "json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "passed", got["status"])
}

// --- run (history) ---

func TestRunList(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "run", "list", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var runs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &runs))
	require.NotEmpty(t, runs)

	stdout, stderr, code = run(t, "--workspace", dir, "run", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "RUN")
	assert.Contains(t, stdout, "create-order-flow")
}

func TestRunShow(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	runs, err := fake.Runs().List(context.Background(), domain.RunFilter{})
	require.NoError(t, err)
	require.NotEmpty(t, runs)
	runID := runs[0].ID

	stdout, stderr, code := run(t, "--workspace", dir, "run", "show", runID)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, runID)
	assert.Contains(t, stdout, "create")

	stdout, stderr, code = run(t, "--workspace", dir, "run", "show", runID, "--step", "create", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var step map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &step))
	assert.Equal(t, "create", step["step_id"])
}

func TestRunShow_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "run", "show", "run_nope", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_RUN_NOT_FOUND", got["code"])
}

func TestRunPin(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	runs, err := fake.Runs().List(context.Background(), domain.RunFilter{})
	require.NoError(t, err)
	runID := runs[0].ID

	stdout, stderr, code := run(t, "--workspace", dir, "run", "pin", runID, "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, true, got["pinned"])

	got2, err := fake.Runs().Get(context.Background(), runID)
	require.NoError(t, err)
	assert.True(t, got2.Pinned)
}

func TestRunPurge(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "run", "purge", "--keep", "0", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.EqualValues(t, 1, got["removed"])
}
