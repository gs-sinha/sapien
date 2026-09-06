package cli_test

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
)

// --- flow list: query filtering ---

func TestFlowList_Query(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "list", "assign", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var flows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &flows))
	require.Len(t, flows, 1)
	assert.Equal(t, "assign-rider-flow", flows[0]["id"])
}

// --- flow create: file-not-found and conflict ---

func TestFlowCreate_FileNotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "flow", "create", filepath.Join(dir, "nope.flow.yaml"), "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
}

func TestFlowCreate_Conflict(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	flowPath := filepath.Join(dir, "dup.flow.yaml")
	require.NoError(t, os.WriteFile(flowPath, []byte(
		"version: 1\nid: create-order-flow\nsteps:\n  - id: create\n    call: order-service.createOrder\n"), 0o644))

	_, stderr, code := run(t, "--workspace", dir, "flow", "create", flowPath, "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_CONFLICT", got["code"])
}

// --- flow delete: not found ---

func TestFlowDelete_NotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "flow", "delete", "no-such-flow", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_FLOW_NOT_FOUND", got["code"])
}

// --- flow validate: reading a path that doesn't exist ---

func TestFlowValidate_PathNotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "flow", "validate", filepath.Join(dir, "missing.flow.yaml"), "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
}

// --- flow run: reading a source path that doesn't exist ---

func TestFlowRun_BySource_FileNotFound(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "flow", "run", filepath.Join(dir, "missing.flow.yaml"), "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
}

// --- flow run --report: unknown kind, json+out file, and a broken --out path ---

func TestFlowRun_Report_UnknownKind(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	_, stderr, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow", "--report", "yaml", "--json")
	assert.Equal(t, 2, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INVALID", got["code"])
}

func TestFlowRun_ReportJSON_OutFile(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	outFile := filepath.Join(dir, "run.json")
	stdout, stderr, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow", "--report", "json", "--out", outFile)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, outFile)

	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "passed", got["status"])
}

func TestFlowRun_Report_OutPathUnwritable(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	badOut := filepath.Join(dir, "no-such-dir", "report.xml")
	_, stderr, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow", "--report", "junit", "--out", badOut, "--json")
	assert.Equal(t, 2, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stderr), &got))
	assert.Equal(t, "E_INTERNAL", got["code"])
}

// --- flow run --report junit: errored + skipped steps, and the FlowID=="" ->
// run.ID fallback in the testsuite name ---

func TestFlowRun_ReportJUnit_ErroredAndSkipped(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		// FlowID deliberately empty: writeJUnit must fall back to run.ID for
		// the testsuite name.
		ID:         "run_explicit_1",
		Status:     domain.RunErrored,
		Started:    now,
		Finished:   now.Add(15 * time.Millisecond),
		DurationMs: 15,
		Steps: []domain.StepResult{
			{StepID: "ok", Operation: "order-service.createOrder", Status: domain.StepPassed,
				Started: now, Finished: now.Add(5 * time.Millisecond)},
			{StepID: "boom", Operation: "order-service.getOrder", Status: domain.StepErrored,
				Error:   &domain.ErrorInfo{Code: "E_HTTP_TRANSPORT", Message: "timeout"},
				Started: now.Add(5 * time.Millisecond), Finished: now.Add(10 * time.Millisecond)},
			{StepID: "skip1", Operation: "rider-service.getRider", Status: domain.StepSkipped,
				Started: now.Add(10 * time.Millisecond), Finished: now.Add(15 * time.Millisecond)},
		},
		Summary: domain.RunSummary{StepsTotal: 3, StepsPassed: 1, StepsErrored: 1, StepsSkipped: 1},
	}

	stdout, stderr, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow", "--report", "junit")
	assert.Equal(t, 2, code, "stderr: %s", stderr)

	var suite struct {
		XMLName   xml.Name `xml:"testsuite"`
		Name      string   `xml:"name,attr"`
		Tests     int      `xml:"tests,attr"`
		Errors    int      `xml:"errors,attr"`
		Skipped   int      `xml:"skipped,attr"`
		Testcases []struct {
			Name  string `xml:"name,attr"`
			Error *struct {
				Message string `xml:"message,attr"`
			} `xml:"error"`
			Skipped *struct{} `xml:"skipped"`
		} `xml:"testcase"`
	}
	require.NoError(t, xml.Unmarshal([]byte(stdout), &suite))
	assert.Equal(t, "run_explicit_1", suite.Name)
	assert.Equal(t, 3, suite.Tests)
	assert.Equal(t, 1, suite.Errors)
	assert.Equal(t, 1, suite.Skipped)

	var boom, skip1 bool
	for _, tc := range suite.Testcases {
		if tc.Name == "boom" {
			boom = true
			require.NotNil(t, tc.Error)
			assert.Equal(t, "timeout", tc.Error.Message)
		}
		if tc.Name == "skip1" {
			skip1 = true
			assert.NotNil(t, tc.Skipped)
		}
	}
	assert.True(t, boom, "expected a testcase named boom")
	assert.True(t, skip1, "expected a testcase named skip1")
}

// --- flow run --watch: every step-status branch of printWatchEvent ---

func TestFlowRun_Watch_AllStepStatuses(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		FlowID:     "create-order-flow",
		Status:     domain.RunFailed,
		Started:    now,
		Finished:   now.Add(20 * time.Millisecond),
		DurationMs: 20,
		Steps: []domain.StepResult{
			{StepID: "passed1", Operation: "order-service.createOrder", Status: domain.StepPassed,
				Response: &domain.ResponseRecord{Status: 201}, Started: now, Finished: now.Add(5 * time.Millisecond)},
			{StepID: "failed1", Operation: "order-service.getOrder", Status: domain.StepFailed,
				Assertions: []domain.AssertionResult{{Expr: "body.ok == true", Passed: false, Message: "was false"}},
				Started:    now.Add(5 * time.Millisecond), Finished: now.Add(10 * time.Millisecond)},
			{StepID: "errored1", Operation: "rider-service.getRider", Status: domain.StepErrored,
				Error:   &domain.ErrorInfo{Code: "E_HTTP_TRANSPORT", Message: "boom"},
				Started: now.Add(10 * time.Millisecond), Finished: now.Add(15 * time.Millisecond)},
			{StepID: "skipped1", Operation: "rider-service.assignRider", Status: domain.StepSkipped,
				Started: now.Add(15 * time.Millisecond), Finished: now.Add(20 * time.Millisecond)},
			{StepID: "pending1", Operation: "order-service.createOrder", Status: domain.StepPending,
				Started: now.Add(15 * time.Millisecond), Finished: now.Add(20 * time.Millisecond)},
		},
		Summary: domain.RunSummary{StepsTotal: 5, StepsPassed: 1, StepsFailed: 1, StepsErrored: 1, StepsSkipped: 1},
	}

	stdout, _, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow", "--watch")
	assert.Equal(t, 1, code) // RunFailed -> exit 1
	assert.Contains(t, stdout, "▸ run started")
	assert.Contains(t, stdout, "✓ passed1  201")
	assert.Contains(t, stdout, "✗ failed1  assertion failed: was false")
	assert.Contains(t, stdout, "✗ errored1  error: boom")
	assert.Contains(t, stdout, "○ skipped1  skipped")
	assert.Contains(t, stdout, "▸ pending1  pending")
}
