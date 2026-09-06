package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/cli"
	"github.com/gs-sinha/sapien/internal/domain"
)

// --- Printer, exercised directly through its exported API (output.go) ---

func TestPrinter_Table_Basic(t *testing.T) {
	var buf bytes.Buffer
	p := cli.NewPrinter(&buf, &buf, false, false)
	p.Table([]string{"NAME", "COUNT"}, [][]string{
		{"a", "1"},
		{"bbbbb", "22"},
	})
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3)
	// Every data column should be padded to the widest cell in that column.
	assert.True(t, strings.HasPrefix(lines[0], "NAME "))
	assert.Contains(t, lines[1], "a    ")
	assert.Contains(t, lines[2], "bbbbb")
}

// TestPrinter_Table_Unicode exercises Table with multi-byte UTF-8 cells. The
// implementation pads by byte length (not rune count), so this mainly proves
// it neither panics nor mis-renders when cells contain non-ASCII text.
func TestPrinter_Table_Unicode(t *testing.T) {
	var buf bytes.Buffer
	p := cli.NewPrinter(&buf, &buf, false, false)
	p.Table([]string{"NAME", "NOTE"}, [][]string{
		{"喜欢", "🚀 launch"},
		{"a", "plain"},
	})
	out := buf.String()
	assert.Contains(t, out, "喜欢")
	assert.Contains(t, out, "🚀 launch")
	assert.Contains(t, out, "plain")
}

// TestPrinter_Table_RowWiderThanHeaders exercises the "extra cell beyond the
// header count" path in both Table and writeRow (the width lookup falls back
// to 0 rather than indexing out of range).
func TestPrinter_Table_RowWiderThanHeaders(t *testing.T) {
	var buf bytes.Buffer
	p := cli.NewPrinter(&buf, &buf, false, false)
	p.Table([]string{"A", "B"}, [][]string{
		{"1", "2", "3"},
	})
	out := buf.String()
	assert.Contains(t, out, "1  2  3")
}

func TestPrinter_Table_NoRows(t *testing.T) {
	var buf bytes.Buffer
	p := cli.NewPrinter(&buf, &buf, false, false)
	p.Table([]string{"ONLY"}, nil)
	assert.Equal(t, "ONLY\n", buf.String())
}

func TestPrinter_Errorf(t *testing.T) {
	var out, errBuf bytes.Buffer
	p := cli.NewPrinter(&out, &errBuf, false, true)
	p.Errorf("boom %d", 42)
	assert.Equal(t, "boom 42\n", errBuf.String())
	assert.Empty(t, out.String())
}

func TestPrinter_JSON_And_Line(t *testing.T) {
	var out bytes.Buffer
	p := cli.NewPrinter(&out, &out, true, false)
	assert.True(t, p.IsJSON())
	require.NoError(t, p.JSON(map[string]int{"a": 1}))
	var got map[string]int
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	assert.Equal(t, 1, got["a"])

	out.Reset()
	p2 := cli.NewPrinter(&out, &out, false, false)
	p2.Line("hello %s", "world")
	assert.Equal(t, "hello world\n", out.String())
}

// --- NO_COLOR / --no-color / CLICOLOR_FORCE / SAPIEN_FORCE_COLOR, exercised
// through `call`'s dim() response header rendering (format_run.go
// printCallHuman). Colour is enabled only when --no-color is not set,
// NO_COLOR is unset, and either stdout is a terminal or one of
// CLICOLOR_FORCE / SAPIEN_FORCE_COLOR is a non-empty value other than "0"
// (output.go ColorEnabled). `run`'s stdout is a bytes.Buffer, never a
// terminal, so these tests drive the force env vars explicitly. ---

func TestCall_Color_PipedDefaultDisabled(t *testing.T) {
	// The bug this whole rule exists to fix: piping to a file/buffer must
	// not print escape codes just because NO_COLOR happens to be unset.
	dir, fake := setupFakeEngine(t)
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("SAPIEN_FORCE_COLOR", "")
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		Status:   domain.RunPassed,
		Started:  now,
		Finished: now.Add(5 * time.Millisecond),
		Steps: []domain.StepResult{{
			StepID:   "step",
			Status:   domain.StepPassed,
			Response: &domain.ResponseRecord{Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: map[string]any{"ok": true}},
			Started:  now, Finished: now.Add(5 * time.Millisecond),
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsPassed: 1},
	}

	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "\x1b[2m")
	assert.Contains(t, stdout, "Content-Type: application/json")
}

func TestCall_Color_ForceEnabled(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "1")
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		Status:   domain.RunPassed,
		Started:  now,
		Finished: now.Add(5 * time.Millisecond),
		Steps: []domain.StepResult{{
			StepID:   "step",
			Status:   domain.StepPassed,
			Response: &domain.ResponseRecord{Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: map[string]any{"ok": true}},
			Started:  now, Finished: now.Add(5 * time.Millisecond),
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsPassed: 1},
	}

	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "\x1b[2m")
	assert.Contains(t, stdout, "Content-Type: application/json")
}

func TestCall_Color_SapienForceColorEnabled(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("SAPIEN_FORCE_COLOR", "yes")
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		Status:   domain.RunPassed,
		Started:  now,
		Finished: now.Add(5 * time.Millisecond),
		Steps: []domain.StepResult{{
			StepID:   "step",
			Status:   domain.StepPassed,
			Response: &domain.ResponseRecord{Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: map[string]any{"ok": true}},
			Started:  now, Finished: now.Add(5 * time.Millisecond),
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsPassed: 1},
	}

	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "\x1b[2m")
}

func TestCall_Color_ForceZeroStaysDisabled(t *testing.T) {
	// CLICOLOR_FORCE=0 is the CLICOLOR convention's explicit "don't force"
	// value, not merely "unset" -- it must not enable colour.
	dir, fake := setupFakeEngine(t)
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "0")
	t.Setenv("SAPIEN_FORCE_COLOR", "0")
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		Status:   domain.RunPassed,
		Started:  now,
		Finished: now.Add(5 * time.Millisecond),
		Steps: []domain.StepResult{{
			StepID:   "step",
			Status:   domain.StepPassed,
			Response: &domain.ResponseRecord{Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: map[string]any{"ok": true}},
			Started:  now, Finished: now.Add(5 * time.Millisecond),
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsPassed: 1},
	}

	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "\x1b[2m")
}

func TestCall_Color_NoColorFlagOverridesForce(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "1")
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		Status:   domain.RunPassed,
		Started:  now,
		Finished: now.Add(5 * time.Millisecond),
		Steps: []domain.StepResult{{
			StepID:   "step",
			Status:   domain.StepPassed,
			Response: &domain.ResponseRecord{Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: map[string]any{"ok": true}},
			Started:  now, Finished: now.Add(5 * time.Millisecond),
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsPassed: 1},
	}

	stdout, stderr, code := run(t, "--workspace", dir, "--no-color", "call", "order-service.createOrder")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "\x1b[2m")
	assert.Contains(t, stdout, "Content-Type: application/json")
}

func TestCall_Color_NoColorEnvVarOverridesForce(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	t.Setenv("NO_COLOR", "1")
	t.Setenv("CLICOLOR_FORCE", "1")
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		Status:   domain.RunPassed,
		Started:  now,
		Finished: now.Add(5 * time.Millisecond),
		Steps: []domain.StepResult{{
			StepID:   "step",
			Status:   domain.StepPassed,
			Response: &domain.ResponseRecord{Status: 200, Headers: map[string]string{"X-Test": "1"}, Body: map[string]any{"ok": true}},
			Started:  now, Finished: now.Add(5 * time.Millisecond),
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsPassed: 1},
	}

	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "\x1b[2m")
}

// --- Printer.Dim / Bold / Red / Green: the centralized ANSI helpers,
// exercised directly against Printer.Color rather than through a command. ---

func TestPrinter_ColorHelpers_Enabled(t *testing.T) {
	var buf bytes.Buffer
	p := cli.NewPrinter(&buf, &buf, false, true)
	assert.Equal(t, "\x1b[2mhi\x1b[0m", p.Dim("hi"))
	assert.Equal(t, "\x1b[1mhi\x1b[0m", p.Bold("hi"))
	assert.Equal(t, "\x1b[31mhi\x1b[0m", p.Red("hi"))
	assert.Equal(t, "\x1b[32mhi\x1b[0m", p.Green("hi"))
}

func TestPrinter_ColorHelpers_Disabled(t *testing.T) {
	var buf bytes.Buffer
	p := cli.NewPrinter(&buf, &buf, false, false)
	assert.Equal(t, "hi", p.Dim("hi"))
	assert.Equal(t, "hi", p.Bold("hi"))
	assert.Equal(t, "hi", p.Red("hi"))
	assert.Equal(t, "hi", p.Green("hi"))
}

// --- cli.ColorEnabled: the standalone rule (output.go), exercised directly
// with an *os.File (a real character-device check) and a bytes.Buffer (never
// a terminal) so the TTY-detection branch itself is covered, not just the
// force-env-var branch that the `call` tests above exercise end-to-end. ---

func TestColorEnabled_BufferNeverATerminal(t *testing.T) {
	var buf bytes.Buffer
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("SAPIEN_FORCE_COLOR", "")
	assert.False(t, cli.ColorEnabled(&buf, false))
}

func TestColorEnabled_RegularFileIsNotATerminal(t *testing.T) {
	// A real *os.File backed by a regular file -- exactly `sapien call ...
	// > out.json` from the bug report -- exercises the Stat()-based branch
	// of isTerminal without requiring an actual attached terminal in CI.
	// (os.DevNull is deliberately not used here: /dev/null is itself a
	// character device, so it would not exercise the "not a terminal"
	// branch this test is after.)
	f, err := os.CreateTemp(t.TempDir(), "sapien-color-test")
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("SAPIEN_FORCE_COLOR", "")
	assert.False(t, cli.ColorEnabled(f, false))

	t.Setenv("CLICOLOR_FORCE", "1")
	assert.True(t, cli.ColorEnabled(f, false))
}

func TestColorEnabled_NoColorFlag(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "1")
	var buf bytes.Buffer
	assert.False(t, cli.ColorEnabled(&buf, true))
}

// --- printCallHuman: no-steps, error-step, and mixed assertion branches ---

func TestCall_Human_NoSteps(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		Status:   domain.RunPassed,
		Started:  now,
		Finished: now,
		Steps:    nil,
		Summary:  domain.RunSummary{},
	}
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Equal(t, "passed\n", stdout)
}

func TestCall_Human_StepErrored(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		Status:   domain.RunErrored,
		Started:  now,
		Finished: now.Add(3 * time.Millisecond),
		Error:    &domain.ErrorInfo{Code: "E_HTTP_TRANSPORT", Message: "connection refused"},
		Steps: []domain.StepResult{{
			StepID:   "call",
			Status:   domain.StepErrored,
			Error:    &domain.ErrorInfo{Code: "E_HTTP_TRANSPORT", Message: "connection refused"},
			Started:  now,
			Finished: now.Add(3 * time.Millisecond),
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsErrored: 1},
	}
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder")
	assert.Equal(t, 2, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "error")
	assert.Contains(t, stdout, "connection refused")
}

func TestCall_Human_MixedAssertions(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		Status:   domain.RunPassed,
		Started:  now,
		Finished: now.Add(5 * time.Millisecond),
		Steps: []domain.StepResult{{
			StepID:   "call",
			Status:   domain.StepPassed,
			Response: &domain.ResponseRecord{Status: 200, Body: map[string]any{"ok": true}},
			Assertions: []domain.AssertionResult{
				{Expr: "status == 200", Passed: true},
				{Expr: "body.ok == false", Passed: false, Message: "expected false"},
			},
			Started: now, Finished: now.Add(5 * time.Millisecond),
		}},
		Summary: domain.RunSummary{StepsTotal: 1, StepsPassed: 1, Assertions: 2, AssertionsFailed: 1},
	}
	stdout, stderr, code := run(t, "--workspace", dir, "call", "order-service.createOrder")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "✓ status == 200")
	assert.Contains(t, stdout, "✗ body.ok == false  expected false")
}

// --- formatMs edge cases: negative durations clamp to zero, and durations
// at/above one second render in seconds (format_run.go formatMs). ---

func TestFlowRun_FormatMs_Edges(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	now := time.Now().UTC()
	fake.RunResult = &domain.Run{
		FlowID:     "create-order-flow",
		Status:     domain.RunPassed,
		Started:    now,
		Finished:   now.Add(1500 * time.Millisecond),
		DurationMs: 1500,
		Steps: []domain.StepResult{
			{
				StepID: "neg", Operation: "order-service.createOrder", Status: domain.StepPassed,
				// No Timings, and Finished before Started: stepDurationMs falls
				// back to Finished.Sub(Started), a negative value.
				Started: now, Finished: now.Add(-10 * time.Millisecond),
			},
			{
				StepID: "sec", Operation: "order-service.getOrder", Status: domain.StepPassed,
				Timings: &domain.Timings{TotalMs: 1500},
				Started: now, Finished: now.Add(1500 * time.Millisecond),
			},
		},
		Summary: domain.RunSummary{StepsTotal: 2, StepsPassed: 2},
	}

	stdout, stderr, code := run(t, "--workspace", dir, "flow", "run", "create-order-flow")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "0 ms")
	assert.Contains(t, stdout, "1.5 s")
}

// --- app.Workspace(): discovering from the current directory (no
// --workspace flag), as opposed to every other test's explicit --workspace. ---

func TestWorkspace_DiscoverFromCwd(t *testing.T) {
	dir := t.TempDir()
	_, _, code := run(t, "init", dir)
	require.Equal(t, 0, code)

	oldwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	require.NoError(t, os.Chdir(dir))

	stdout, stderr, code := run(t, "env", "list", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var envs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &envs))
	require.Len(t, envs, 1)
}

// --- "--json always prints a single JSON document" across a broad sample
// of commands. ---

func TestJSON_SingleDocumentAcrossCommands(t *testing.T) {
	dir, _ := setupFakeEngine(t)

	cases := [][]string{
		{"version", "--json"},
		{"--workspace", dir, "env", "list", "--json"},
		{"--workspace", dir, "env", "show", "local", "--json"},
		{"--workspace", dir, "service", "list", "--json"},
		{"--workspace", dir, "memory", "list", "--json"},
		{"--workspace", dir, "memory", "search", "order", "--json"},
		{"--workspace", dir, "run", "list", "--json"},
		{"--workspace", dir, "flow", "list", "--json"},
		{"--workspace", dir, "docs", "list", "--json"},
		{"--workspace", dir, "search", "order", "--json"},
		{"--workspace", dir, "context", "test intent", "--json"},
		{"--workspace", dir, "describe", "order-service.createOrder", "--json"},
		{"--workspace", dir, "secret", "list", "--json"},
		{"--workspace", dir, "reindex", "--json"},
	}

	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			stdout, stderr, code := run(t, args...)
			require.Equal(t, 0, code, "args %v, stderr: %s", args, stderr)

			dec := json.NewDecoder(strings.NewReader(stdout))
			var v any
			require.NoError(t, dec.Decode(&v), "args %v: stdout not valid JSON: %s", args, stdout)
			assert.False(t, dec.More(), "args %v: stdout had more than one JSON document: %s", args, stdout)
		})
	}
}
