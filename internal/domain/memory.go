package domain

import "time"

// MemoryType classifies a memory for retrieval. It never gates capture.
type MemoryType string

const (
	MemoryNote        MemoryType = "note"
	MemorySemantic    MemoryType = "semantic"
	MemoryBehavioral  MemoryType = "behavioral"
	MemoryTesting     MemoryType = "testing"
	MemoryInvariant   MemoryType = "invariant"
	MemoryEnvironment MemoryType = "environment"
	MemoryGotcha      MemoryType = "gotcha"
)

// MemoryScope controls visibility and where the memory is stored (PLAN §12).
type MemoryScope string

const (
	ScopePersonal  MemoryScope = "personal"  // SQLite only
	ScopeWorkspace MemoryScope = "workspace" // <workspace>/memories/*.md
	ScopeService   MemoryScope = "service"   // <service>/api/memories/*.md
	ScopeFlow      MemoryScope = "flow"      // stored with its owner; visibility narrowed to the flow
)

// MemoryStatus is the lifecycle state.
type MemoryStatus string

const (
	MemoryActive     MemoryStatus = "active"
	MemoryPromoted   MemoryStatus = "promoted"
	MemorySuperseded MemoryStatus = "superseded"
	MemoryDeprecated MemoryStatus = "deprecated"
	MemoryDisputed   MemoryStatus = "disputed"
)

// ErrorRef identifies a documented error outcome.
type ErrorRef struct {
	Operation string `yaml:"operation,omitempty" json:"operation,omitempty"`
	Status    int    `yaml:"status,omitempty" json:"status,omitempty"`
	Code      string `yaml:"code,omitempty" json:"code,omitempty"`
}

// Subject is what a memory is about. Any combination of keys may be set.
type Subject struct {
	Service     string    `yaml:"service,omitempty" json:"service,omitempty"`
	Operation   string    `yaml:"operation,omitempty" json:"operation,omitempty"`
	Field       string    `yaml:"field,omitempty" json:"field,omitempty"`   // field path; needs Operation or Schema
	Schema      string    `yaml:"schema,omitempty" json:"schema,omitempty"` // "<service>.<ComponentName>"
	Flow        string    `yaml:"flow,omitempty" json:"flow,omitempty"`
	Step        string    `yaml:"step,omitempty" json:"step,omitempty"`
	Run         string    `yaml:"run,omitempty" json:"run,omitempty"`
	Environment string    `yaml:"environment,omitempty" json:"environment,omitempty"`
	Error       *ErrorRef `yaml:"error,omitempty" json:"error,omitempty"`
	Concept     string    `yaml:"concept,omitempty" json:"concept,omitempty"`
}

// IsZero reports whether no subject key is set.
func (s Subject) IsZero() bool {
	return s.Service == "" && s.Operation == "" && s.Field == "" && s.Schema == "" && s.Flow == "" &&
		s.Step == "" && s.Run == "" && s.Environment == "" && s.Error == nil && s.Concept == ""
}

// ResolvedSubject is the snapshot taken when a subject was last resolved against the catalog.
type ResolvedSubject struct {
	Method        string    `yaml:"method,omitempty" json:"method,omitempty"`
	Path          string    `yaml:"path,omitempty" json:"path,omitempty"`
	OperationHash string    `yaml:"operation_hash,omitempty" json:"operation_hash,omitempty"`
	ResolvedAt    time.Time `yaml:"resolved_at,omitempty" json:"resolved_at,omitempty"`
	Unresolved    bool      `yaml:"unresolved,omitempty" json:"unresolved,omitempty"`
}

// MemorySource is provenance.
type MemorySource struct {
	Kind   string `yaml:"kind" json:"kind"`                         // user | agent | run | import | documentation
	Client string `yaml:"client,omitempty" json:"client,omitempty"` // agent: MCP client name
	Model  string `yaml:"model,omitempty" json:"model,omitempty"`
	RunID  string `yaml:"run_id,omitempty" json:"run_id,omitempty"`
	StepID string `yaml:"step_id,omitempty" json:"step_id,omitempty"`
	Path   string `yaml:"path,omitempty" json:"path,omitempty"` // import: origin path; documentation: file
}

// Memory is a unit of captured knowledge (PLAN §10).
type Memory struct {
	ID       string           `yaml:"id" json:"id"` // "mem_<ULID>"
	Type     MemoryType       `yaml:"type,omitempty" json:"type"`
	Scope    MemoryScope      `yaml:"scope" json:"scope"`
	Subject  Subject          `yaml:"subject,omitempty" json:"subject"`
	Tags     []string         `yaml:"tags,omitempty" json:"tags,omitempty"`
	Source   MemorySource     `yaml:"source" json:"source"`
	Status   MemoryStatus     `yaml:"status,omitempty" json:"status"`
	Created  time.Time        `yaml:"created" json:"created"`
	Updated  time.Time        `yaml:"updated" json:"updated"`
	Resolved *ResolvedSubject `yaml:"resolved,omitempty" json:"resolved,omitempty"`
	Text     string           `yaml:"-" json:"text"`                // Markdown body
	FilePath string           `yaml:"-" json:"file_path,omitempty"` // empty for personal scope
	Hash     string           `yaml:"-" json:"hash,omitempty"`
}

// MemoryQuery selects memories.
type MemoryQuery struct {
	Text      string
	Subjects  []Subject // structural association; any match
	Scope     MemoryScope
	Type      MemoryType
	Service   string
	Operation string
	Flow      string
	Limit     int
	MinScore  float64
}

// ScoredMemory is a retrieval result.
type ScoredMemory struct {
	Memory  Memory   `json:"memory"`
	Score   float64  `json:"score"`
	Reasons []string `json:"reasons,omitempty"` // e.g. "operation match", "lexical"
}
