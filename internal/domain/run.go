package domain

import "time"

// RunStatus is the state of a run.
type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunPassed    RunStatus = "passed"
	RunFailed    RunStatus = "failed"  // an assertion or until-timeout failed
	RunErrored   RunStatus = "errored" // resolution, transport, or engine error
	RunCancelled RunStatus = "cancelled"
)

// StepStatus is the state of one step.
type StepStatus string

const (
	StepPending    StepStatus = "pending"
	StepResolving  StepStatus = "resolving"
	StepRequesting StepStatus = "requesting"
	StepPolling    StepStatus = "polling"
	StepAsserting  StepStatus = "asserting"
	StepPassed     StepStatus = "passed"
	StepFailed     StepStatus = "failed"
	StepErrored    StepStatus = "errored"
	StepSkipped    StepStatus = "skipped"
	StepCancelled  StepStatus = "cancelled"
)

// Run is one execution of a flow (a single `call` is a one-step run).
type Run struct {
	ID              string            `json:"id"`
	FlowID          string            `json:"flow_id,omitempty"`
	FlowSnapshot    string            `json:"flow_snapshot,omitempty"` // YAML at run start
	Environment     string            `json:"environment"`
	Inputs          map[string]any    `json:"inputs,omitempty"`
	Status          RunStatus         `json:"status"`
	Started         time.Time         `json:"started"`
	Finished        time.Time         `json:"finished,omitempty"`
	DurationMs      int64             `json:"duration_ms,omitempty"`
	Steps           []StepResult      `json:"steps,omitempty"`
	Error           *ErrorInfo        `json:"error,omitempty"`
	Summary         RunSummary        `json:"summary"`
	Pinned          bool              `json:"pinned,omitempty"`
	Trigger         string            `json:"trigger,omitempty"`          // cli | ui | mcp | ci
	OperationHashes map[string]string `json:"operation_hashes,omitempty"` // catalog hashes of operations used
	// ResumedFrom is the run whose step results this run reused (see
	// engine.RunOptions.ResumeFrom).
	ResumedFrom string `json:"resumed_from,omitempty"`
}

// RunSummary is the one-line view of a run.
type RunSummary struct {
	StepsTotal       int `json:"steps_total"`
	StepsPassed      int `json:"steps_passed"`
	StepsFailed      int `json:"steps_failed"`
	StepsErrored     int `json:"steps_errored"`
	StepsSkipped     int `json:"steps_skipped"`
	Assertions       int `json:"assertions"`
	AssertionsFailed int `json:"assertions_failed"`
	// AssertionsWarned counts soft assertions that did not hold; they never
	// fail a step or a run.
	AssertionsWarned int `json:"assertions_warned,omitempty"`
}

// ErrorInfo is a serialized structured error.
type ErrorInfo struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// StepResult is the persisted outcome of one step.
type StepResult struct {
	StepID    string     `json:"step_id"`
	Index     int        `json:"index"`
	Operation string     `json:"operation,omitempty"`
	Status    StepStatus `json:"status"`
	Attempts  int        `json:"attempts,omitempty"` // >1 when polled
	// SkipReason names why Status is skipped: "when" (this step's `when`
	// evaluated false) is the only reason set today; a step skipped because
	// an earlier step failed/errored, or because it fell outside a resumed
	// run's window, leaves this empty.
	SkipReason string            `json:"skip_reason,omitempty"`
	Request    *RequestRecord    `json:"request,omitempty"`
	Response   *ResponseRecord   `json:"response,omitempty"`
	Timings    *Timings          `json:"timings,omitempty"`
	Assertions []AssertionResult `json:"assertions,omitempty"`
	Out        map[string]any    `json:"out,omitempty"` // extracted values
	Error      *ErrorInfo        `json:"error,omitempty"`
	Started    time.Time         `json:"started,omitempty"`
	Finished   time.Time         `json:"finished,omitempty"`
	// Phase is "setup", "steps", or "teardown" (empty means "steps").
	Phase string `json:"phase,omitempty"`
	// Reused marks a step whose request, response, and extracted values were
	// copied from ReusedFromRun instead of being executed (a resumed run).
	Reused        bool   `json:"reused,omitempty"`
	ReusedFromRun string `json:"reused_from_run,omitempty"`
	// Warnings carries non-fatal notes such as "step definition changed since
	// the run it was reused from".
	Warnings []string `json:"warnings,omitempty"`
}

// RequestRecord is the (redacted) request as sent.
type RequestRecord struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    any               `json:"body,omitempty"`
	BodyRaw string            `json:"body_raw,omitempty"`
}

// ResponseRecord is the (redacted, capped) response.
type ResponseRecord struct {
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      any               `json:"body,omitempty"` // parsed JSON when possible
	BodyRaw   string            `json:"body_raw,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
	Size      int64             `json:"size"`
}

// Timings are request phase durations in milliseconds.
type Timings struct {
	DNSMs     float64 `json:"dns_ms"`
	ConnectMs float64 `json:"connect_ms"`
	TLSMs     float64 `json:"tls_ms"`
	TTFBMs    float64 `json:"ttfb_ms"`
	TotalMs   float64 `json:"total_ms"`
}

// AssertionResult records one evaluated assertion.
type AssertionResult struct {
	Expr    string `json:"expr"`
	Passed  bool   `json:"passed"`
	Actual  any    `json:"actual,omitempty"` // evaluated left-hand value when derivable
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"` // evaluation error, distinct from a false result
	// Soft mirrors the assertion's soft flag: a false result is a warning,
	// not a failure.
	Soft bool `json:"soft,omitempty"`
}

// RunFilter selects runs.
type RunFilter struct {
	FlowID    string
	Status    RunStatus
	Operation string
	Limit     int
	Offset    int
}
