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
	// Tier is where Path sits: TierLocal, TierWorkspace or TierService,
	// derived from the path when the file is read.
	Tier string `yaml:"-" json:"tier,omitempty"`
	// Shipped is the workspace-tier file's state in the workspace repository
	// (the Ship* constants); "" for other tiers.
	Shipped string `yaml:"-" json:"shipped,omitempty"`
	// Folder is the subfolder of the owning directory Path sits in (PLAN
	// §34f item 6): "" at the root, "/"-separated otherwise. Derived from
	// Path when the file is read; not omitempty so "" (root) always shows
	// up on the wire. A plain Update never changes it -- only Store.MoveFolder
	// does.
	Folder string `yaml:"-" json:"folder"`
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
	Text      string `json:"text,omitempty"` // substring over id, description, tags, folder
	Limit     int    `json:"limit,omitempty"`
	// Folder restricts results to that folder and everything below it
	// (PLAN §34f item 4): "" (the default) applies no folder filter.
	Folder string `json:"folder,omitempty"`
}

// RequestExampleSource says where a RequestExample's payload came from, so a
// caller knows how much to trust it: a verified example was really sent and
// really worked, a saved one was written by hand, a contract one comes from
// the service's own openapi.yaml, and a synthesized one is placeholders
// derived from the schema and has never been near the service.
type RequestExampleSource string

const (
	RequestExampleVerified    RequestExampleSource = "verified"
	RequestExampleSaved       RequestExampleSource = "saved"
	RequestExampleContract    RequestExampleSource = "contract"
	RequestExampleSynthesized RequestExampleSource = "schema"
)

// RequestExample is a ready-to-send request for one operation: the thing a
// caller would otherwise have to compile out of the schema by hand. Every
// operation has one -- the best available of a verified example, a saved
// example, the contract's own `example:`, and a payload synthesized from the
// request schema -- so no surface has to answer "what does a call to this
// look like?" with a schema and a shrug.
type RequestExample struct {
	Operation string               `json:"operation"`
	Source    RequestExampleSource `json:"source"`
	SourceID  string               `json:"source_id,omitempty"` // example id, or the contract example's name
	// Input binds path/query/header params by name, like a flow step's
	// `input:` and a SavedExample's Input.
	Input   map[string]any    `json:"input,omitempty"`
	Body    any               `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Note warns the reader about what the payload is not: placeholder
	// values, omitted optional fields. Empty for a verified example.
	Note string `json:"note,omitempty"`
}
