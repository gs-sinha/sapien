package cli

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/gs-sinha/sapien/internal/diagnose"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// formatMs renders a millisecond duration the way PLAN §21 examples show it:
// "84 ms" below one second, "1.2 s" at or above it.
func formatMs(ms float64) string {
	if ms < 0 {
		ms = 0
	}
	if ms < 1000 {
		return fmt.Sprintf("%d ms", int64(math.Round(ms)))
	}
	return fmt.Sprintf("%.1f s", ms/1000)
}

// stepDurationMs prefers the measured total latency, falling back to
// Finished-Started when timings weren't recorded.
func stepDurationMs(st domain.StepResult) float64 {
	if st.Timings != nil && st.Timings.TotalMs > 0 {
		return st.Timings.TotalMs
	}
	if !st.Started.IsZero() && !st.Finished.IsZero() {
		return float64(st.Finished.Sub(st.Started).Milliseconds())
	}
	return 0
}

// dim wraps s in the ANSI "faint" escape when the printer has color enabled,
// for the response-header lines in `sapien call`'s human output.
func dim(p *Printer, s string) string {
	if !p.Color {
		return s
	}
	return "\x1b[2m" + s + "\x1b[0m"
}

// prettyJSON renders v as an indented JSON document. encoding/json already
// gives stable output: struct fields marshal in declaration order and map
// keys are sorted lexically.
func prettyJSON(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// compactJSON renders v as single-line JSON, or "" for a nil/unmarshalable
// value, for table cells (PLAN §21 `run show --bodies`).
func compactJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// assertionSummary renders "<passed>/<total>" for a step's assertions, or ""
// when the step made none.
func assertionSummary(assertions []domain.AssertionResult) string {
	if len(assertions) == 0 {
		return ""
	}
	passed, soft := 0, 0
	for _, a := range assertions {
		switch {
		case a.Passed:
			passed++
		case a.Soft:
			soft++
		}
	}
	out := fmt.Sprintf("%d/%d", passed, len(assertions))
	if soft > 0 {
		out += fmt.Sprintf(" (%d soft)", soft)
	}
	return out
}

// printSoftLines prints soft-assertion mismatches and flips since the
// previous run of the flow, after the run table.
func printSoftLines(p *Printer, run *domain.Run, changes []diagnose.SoftChange) {
	for _, l := range diagnose.SoftLines(run, changes) {
		p.Line("%s", l)
	}
}

// failedAssertionMessages collects a human-readable line per failed
// assertion (and the step's own error, if any), shared by the human step
// detail view and the JUnit writer.
func failedAssertionMessages(st domain.StepResult) []string {
	var out []string
	for _, a := range st.Assertions {
		if a.Passed {
			continue
		}
		msg := a.Expr
		if a.Message != "" {
			msg = a.Message
		}
		if a.Error != "" {
			msg = msg + ": " + a.Error
		}
		out = append(out, msg)
	}
	if st.Error != nil {
		out = append(out, st.Error.Message)
	}
	return out
}

// runSummaryLine renders `flow run`'s final line: "passed · 3/3 steps · 1.2 s
// · run run_01J…" (PLAN §21).
func runSummaryLine(run *domain.Run) string {
	return fmt.Sprintf("%s · %d/%d steps · %s · run %s",
		run.Status, run.Summary.StepsPassed, run.Summary.StepsTotal, formatMs(float64(run.DurationMs)), run.ID)
}

// runWithHints wraps a run for --json output that adds diagnostic hints
// (PLAN feedback: "a run-failure hint mapping an error code to the
// operations/docs likely to explain it") without breaking existing JSON
// consumers. domain.Run is embedded anonymously, so its own fields (id,
// status, steps, ...) marshal at exactly the top level they always have;
// Hints is simply one more field, omitted entirely for a run that has none
// (every passed run, and any run diagnose found nothing for).
type runWithHints struct {
	domain.Run
	Hints []diagnose.Hint `json:"hints,omitempty"`
}

// printHints renders the "Might explain it:" block shared by `sapien call`,
// `sapien flow run`, and `sapien run show` for a failed or errored run: one
// line per hint (kind, title, and the token it matched on), followed by the
// concrete command to open it (a doc hint's `sapien docs show`, a memory
// hint's `sapien memory show`). It prints nothing when hints is empty, so
// callers can invoke it unconditionally.
func printHints(p *Printer, hints []diagnose.Hint) {
	if len(hints) == 0 {
		return
	}
	p.Line("")
	p.Line("Might explain it:")
	for _, h := range hints {
		line := fmt.Sprintf("  %-8s %s", string(h.Kind), h.Title)
		if h.MatchedOn != "" {
			line += fmt.Sprintf("  (matched: %s)", h.MatchedOn)
		}
		p.Line("%s", line)
		if open := hintOpenCommand(h); open != "" {
			p.Line("           %s", open)
		}
	}
}

// hintOpenCommand renders the CLI command that opens h's source, or "" for
// a hint kind with nothing further to open (a contract hint's description
// is already inline in the run's own response).
func hintOpenCommand(h diagnose.Hint) string {
	switch h.Kind {
	case diagnose.KindDoc:
		cmd := fmt.Sprintf("sapien docs show %s %s", h.Ref.Service, h.Ref.Path)
		if h.Ref.Section != "" {
			cmd += fmt.Sprintf(" --section %q", h.Ref.Section)
		}
		return cmd
	case diagnose.KindMemory:
		return fmt.Sprintf("sapien memory show %s", h.Ref.MemoryID)
	default:
		return ""
	}
}

// runExitError maps a completed run's terminal status to the error whose
// errs.ExitCode determines the process exit code (PLAN §21, §29): passed
// exits 0 (nil), failed (an assertion or until-timeout) exits 1, anything
// else (errored, cancelled) exits 2. The engine has no dedicated "run
// failed" error type, so this inspects run.Status directly, as PLAN §9
// documents ("Distinguished everywhere ... CLI exit code 1 vs 2").
func runExitError(run *domain.Run) error {
	switch run.Status {
	case domain.RunPassed:
		return nil
	case domain.RunFailed:
		msg := fmt.Sprintf("run %s failed", run.ID)
		if run.Summary.AssertionsFailed > 0 {
			msg = fmt.Sprintf("run %s failed: %d assertion(s) failed", run.ID, run.Summary.AssertionsFailed)
		}
		return errs.New(errs.AssertionFailed, "%s", msg).WithDetail("run_id", run.ID)
	default:
		if run.Error != nil {
			return errs.New(errs.Code(run.Error.Code), "%s", run.Error.Message).WithDetail("run_id", run.ID)
		}
		return errs.New(errs.Internal, "run %s %s", run.ID, run.Status).WithDetail("run_id", run.ID)
	}
}

// checkProduction returns errs.ProductionBlocked when envName is a
// production environment and the caller has not passed --allow-production
// (PLAN §20). It looks the environment up through the engine so `call` and
// `flow run` share one code path, and never blocks on its own lookup
// failure — the operation itself will surface a clearer error for an
// unknown environment.
func checkProduction(ctx context.Context, eng engine.Engine, envName string, allowProduction bool) error {
	if envName == "" || allowProduction {
		return nil
	}
	env, err := eng.Envs().Get(ctx, envName)
	if err != nil {
		return nil
	}
	if env.Production {
		return errs.New(errs.ProductionBlocked, "environment %q is a production environment", envName).
			WithHint("pass --allow-production to run against it").
			WithDetail("environment", envName)
	}
	return nil
}

// parseParams parses "-p/-i key=value" pairs into a flat map. Each value is
// JSON-decoded when it parses (numbers, booleans, null, objects, arrays),
// else kept as a literal string (PLAN §21).
func parseParams(pairs []string) (map[string]any, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(pairs))
	for _, pair := range pairs {
		key, val, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return nil, errs.New(errs.Invalid, "invalid %q: want key=value", pair)
		}
		out[key] = parseParamValue(val)
	}
	return out, nil
}

func parseParamValue(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		return v
	}
	return s
}

// parseHeaders parses "-H key:value" pairs into a header map.
func parseHeaders(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		key, val, ok := strings.Cut(pair, ":")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, errs.New(errs.Invalid, "invalid header %q: want key:value", pair)
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return out, nil
}

// parseBody implements --body json|@file (PLAN §21): a literal JSON string,
// or @path to read the body from a file. An empty spec means no body.
func parseBody(spec string) (any, error) {
	if spec == "" {
		return nil, nil
	}
	raw := spec
	if strings.HasPrefix(spec, "@") {
		path := strings.TrimPrefix(spec, "@")
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, errs.Wrap(errs.Invalid, err, "reading --body file %s", path)
		}
		raw = string(data)
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, errs.Wrap(errs.Invalid, err, "parsing --body as JSON")
	}
	return v, nil
}

// printCallHuman renders `sapien call`'s human-mode output: a status line,
// dim response headers, the pretty-printed body, then any assertion results
// (PLAN §21).
func printCallHuman(p *Printer, run *domain.Run) {
	if len(run.Steps) == 0 {
		p.Line("%s", run.Status)
		return
	}
	st := run.Steps[0]

	switch {
	case st.Response != nil:
		p.Line("%d %s  %s", st.Response.Status, http.StatusText(st.Response.Status), formatMs(stepDurationMs(st)))
	case st.Error != nil:
		p.Line("error  %s", formatMs(stepDurationMs(st)))
	default:
		p.Line("%s", st.Status)
	}

	if st.Response != nil && len(st.Response.Headers) > 0 {
		for _, k := range sortedKeys(st.Response.Headers) {
			p.Line("%s", dim(p, fmt.Sprintf("%s: %s", k, st.Response.Headers[k])))
		}
	}

	if st.Response != nil && st.Response.Body != nil {
		body, err := prettyJSON(st.Response.Body)
		if err == nil {
			p.Line("")
			p.Line("%s", body)
		}
	}

	if len(st.Assertions) > 0 {
		p.Line("")
		for _, a := range st.Assertions {
			mark := "✓"
			if !a.Passed {
				mark = "✗"
			}
			line := fmt.Sprintf("%s %s", mark, a.Expr)
			if !a.Passed && a.Message != "" {
				line += "  " + a.Message
			}
			p.Line("%s", line)
		}
	}

	if st.Error != nil {
		p.Line("")
		p.Line("error: %s", st.Error.Message)
	}
}

// printFlowRunHuman renders `sapien flow run`'s default output: the step
// table, failed-assertion/error detail, and the final summary line.
func printFlowRunHuman(p *Printer, run *domain.Run) {
	rows := make([][]string, 0, len(run.Steps))
	for _, st := range run.Steps {
		httpStatus := ""
		if st.Response != nil {
			httpStatus = strconv.Itoa(st.Response.Status)
		}
		rows = append(rows, []string{
			stepIDLabel(st), st.Operation, stepStatusLabel(st), httpStatus,
			formatMs(stepDurationMs(st)), assertionSummary(st.Assertions),
		})
	}
	p.Table([]string{"STEP", "OPERATION", "STATUS", "HTTP", "LATENCY", "ASSERTIONS"}, rows)

	for _, st := range run.Steps {
		for _, a := range st.Assertions {
			if a.Passed {
				continue
			}
			p.Line("")
			p.Line("✗ %s  %s", st.StepID, a.Expr)
			if a.Actual != nil {
				if actual, err := prettyJSON(a.Actual); err == nil {
					p.Line("  actual: %s", actual)
				}
			}
			if a.Message != "" {
				p.Line("  message: %s", a.Message)
			}
			if a.Error != "" {
				p.Line("  error: %s", a.Error)
			}
		}
		if st.Error != nil {
			p.Line("")
			p.Line("✗ %s  error: %s", st.StepID, st.Error.Message)
		}
	}

	p.Line("")
	p.Line("%s", runSummaryLine(run))
}

// printWatchEvent renders one live event under `flow run --watch`, driven by
// engine.RunOptions.Observer (PLAN §21): "▸ create requesting",
// "✓ create 201 84 ms", "✗ rider assertion failed: ...".
func printWatchEvent(p *Printer, ev domain.Event) {
	switch ev.Type {
	case domain.EventRunStarted:
		p.Line("▸ run started")
	case domain.EventRunStep:
		st, ok := ev.Payload.(domain.StepResult)
		if !ok {
			return
		}
		switch st.Status {
		case domain.StepPassed:
			httpStatus := ""
			if st.Response != nil {
				httpStatus = strconv.Itoa(st.Response.Status)
			}
			p.Line("✓ %s  %s  %s", st.StepID, httpStatus, formatMs(stepDurationMs(st)))
		case domain.StepFailed:
			msg := ""
			if fails := failedAssertionMessages(st); len(fails) > 0 {
				msg = fails[0]
			}
			p.Line("✗ %s  assertion failed: %s", st.StepID, msg)
		case domain.StepErrored:
			msg := ""
			if st.Error != nil {
				msg = st.Error.Message
			}
			p.Line("✗ %s  error: %s", st.StepID, msg)
		case domain.StepSkipped:
			p.Line("○ %s  skipped", st.StepID)
		default:
			p.Line("▸ %s  %s", st.StepID, st.Status)
		}
	case domain.EventRunFinished:
		// The final summary line is printed once, after RunFlow/RunFlowSource
		// returns, from the returned *domain.Run — not from this event.
	}
}

// sortedKeys returns m's keys, sorted, for deterministic header rendering.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- JUnit report (PLAN §21 `--report junit`) ---

type junitTestsuite struct {
	XMLName   xml.Name        `xml:"testsuite"`
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Errors    int             `xml:"errors,attr"`
	Skipped   int             `xml:"skipped,attr"`
	Time      string          `xml:"time,attr"`
	Testcases []junitTestcase `xml:"testcase"`
}

type junitTestcase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Error     *junitError   `xml:"error,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

type junitError struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

type junitSkipped struct{}

// writeJUnit writes run as one JUnit testsuite (one testcase per step) to w.
func writeJUnit(w io.Writer, run *domain.Run) error {
	name := run.FlowID
	if name == "" {
		name = run.ID
	}
	suite := junitTestsuite{
		Name:  name,
		Tests: len(run.Steps),
		Time:  fmt.Sprintf("%.3f", float64(run.DurationMs)/1000),
	}
	for _, st := range run.Steps {
		tc := junitTestcase{
			Name:      st.StepID,
			Classname: st.Operation,
			Time:      fmt.Sprintf("%.3f", stepDurationMs(st)/1000),
		}
		switch st.Status {
		case domain.StepFailed:
			suite.Failures++
			msgs := failedAssertionMessages(st)
			tc.Failure = &junitFailure{Message: strings.Join(msgs, "; "), Text: strings.Join(msgs, "\n")}
		case domain.StepErrored:
			suite.Errors++
			msg := ""
			if st.Error != nil {
				msg = st.Error.Message
			}
			tc.Error = &junitError{Message: msg, Text: msg}
		case domain.StepSkipped, domain.StepCancelled:
			suite.Skipped++
			tc.Skipped = &junitSkipped{}
		}
		suite.Testcases = append(suite.Testcases, tc)
	}

	if _, err := w.Write([]byte(xml.Header)); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(suite); err != nil {
		return err
	}
	_, err := w.Write([]byte("\n"))
	return err
}

// stepStatusLabel renders a step's status for tables, marking steps a
// resumed run copied from an earlier run and steps outside the main phase.
func stepStatusLabel(st domain.StepResult) string {
	label := string(st.Status)
	if st.Reused {
		label += " (reused)"
	}
	return label
}

// stepIDLabel prefixes setup and teardown steps with their phase so a run
// table reads in execution order without a separate column.
func stepIDLabel(st domain.StepResult) string {
	if st.Phase != "" && st.Phase != "steps" {
		return st.Phase + ":" + st.StepID
	}
	return st.StepID
}
