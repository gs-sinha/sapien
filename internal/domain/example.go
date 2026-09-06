package domain

import "time"

// ExampleScope is where an example file lives and therefore who sees it:
// workspace examples sit in <workspace>/examples/ (local unless the
// workspace is a git repo); service examples sit in the service's own
// api/examples/ and travel with the code. The same asymmetry as memories.
type ExampleScope string

const (
	ExampleScopeWorkspace ExampleScope = "workspace"
	ExampleScopeService   ExampleScope = "service"
)

// SavedExample is a saved, reusable request for one operation: the payload a
// human or agent found to work, kept at the workspace layer so flows, the
// CLI, MCP hosts, and a UI can replay it without rediscovering it. Bodies
// and inputs may carry `${inputs.x}` templates so a flow can parametrise
// an example.
type SavedExample struct {
	Version     int          `yaml:"version" json:"version"`
	ID          string       `yaml:"id" json:"id"` // default: file name stem
	Operation   string       `yaml:"operation" json:"operation"`
	Description string       `yaml:"description,omitempty" json:"description,omitempty"`
	Scope       ExampleScope `yaml:"-" json:"scope"`
	Service     string       `yaml:"-" json:"service"` // derived from Operation

	Input   map[string]any    `yaml:"input,omitempty" json:"input,omitempty"` // path/query/header params, bound by name
	Body    any               `yaml:"body,omitempty" json:"body,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`

	Expect   *ExampleExpect   `yaml:"expect,omitempty" json:"expect,omitempty"`
	Verified *ExampleVerified `yaml:"verified,omitempty" json:"verified,omitempty"`
	Tags     []string         `yaml:"tags,omitempty" json:"tags,omitempty"`

	Created time.Time `yaml:"created,omitempty" json:"created"`
	Updated time.Time `yaml:"updated,omitempty" json:"updated"`
	Path    string    `yaml:"-" json:"path,omitempty"` // file on disk
}

// ExampleExpect is the response the example produced when it was saved:
// the status and a possibly trimmed body, so a reader knows what "good"
// looks like. It is documentation, not an assertion.
type ExampleExpect struct {
	Status int `yaml:"status,omitempty" json:"status,omitempty"`
	Body   any `yaml:"body,omitempty" json:"body,omitempty"`
}

// ExampleVerified records that the example came from a real run, so a
// tested example can be told from a hand-written one. It is set only by
// save-from-run paths, never by hand.
type ExampleVerified struct {
	Env    string        `yaml:"env,omitempty" json:"env,omitempty"`
	RunID  string        `yaml:"run,omitempty" json:"run,omitempty"`
	StepID string        `yaml:"step,omitempty" json:"step,omitempty"`
	At     time.Time     `yaml:"at,omitempty" json:"at"`
	Source *MemorySource `yaml:"by,omitempty" json:"by,omitempty"` // user | agent{client}
}

// ExampleQuery filters List.
type ExampleQuery struct {
	Operation string `json:"operation,omitempty"`
	Service   string `json:"service,omitempty"`
	Tag       string `json:"tag,omitempty"`
	Text      string `json:"text,omitempty"` // substring over id, description, tags
	Limit     int    `json:"limit,omitempty"`
}
