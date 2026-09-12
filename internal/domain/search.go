package domain

// SearchResult is one operation hit.
type SearchResult struct {
	Operation Operation   `json:"operation"`
	Score     float64     `json:"score"`
	MatchedOn []string    `json:"matched_on,omitempty"` // "op_id", "path", "summary", "field:qcomSkill", ...
	Tasks     []TaskMatch `json:"tasks,omitempty"`
}

// DocSearchResult is one doc-section hit.
type DocSearchResult struct {
	Service   string   `json:"service"`
	DocID     string   `json:"doc_id"`
	Path      string   `json:"path"`
	Title     string   `json:"title"`
	SectionID string   `json:"section_id"`
	Heading   string   `json:"heading"`
	Snippet   string   `json:"snippet"`
	Refs      []DocRef `json:"refs,omitempty"`
	Score     float64  `json:"score"`
}

// SearchOptions filter a search.
type SearchOptions struct {
	Service           string
	Method            string
	Limit             int
	IncludeDeprecated bool
	// Deterministic disables mutable/optional rankers (usage feedback,
	// semantic vectors, and experimental doc fusion). Task retrieval tests
	// use it so the same service.yaml produces the same diagnostics.
	Deterministic bool
}

// ContextRequest asks the context builder for an agent-ready bundle (PLAN §14).
type ContextRequest struct {
	Intent       string   `json:"intent"`
	Operations   []string `json:"operations,omitempty"`
	Flow         string   `json:"flow,omitempty"`
	Environment  string   `json:"environment,omitempty"`
	BudgetTokens int      `json:"budget_tokens,omitempty"` // default 8000
}

// Tier labels where a context item sits in the knowledge hierarchy (PRD §43).
type Tier string

const (
	TierContract      Tier = "contract"
	TierDocumentation Tier = "documentation"
	TierMemory        Tier = "memory"
	TierRun           Tier = "run"
)

// OperationContext is the compact rendering of an operation for agents.
type OperationContext struct {
	Tier        Tier     `json:"tier"`
	ID          string   `json:"id"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Summary     string   `json:"summary,omitempty"`
	Description string   `json:"description,omitempty"`
	Params      []string `json:"params,omitempty"`   // "riderId (path, string, required) — ..."
	Body        []string `json:"body,omitempty"`     // "customerId: string — ..."
	Response    []string `json:"response,omitempty"` // primary success response fields
	Security    []string `json:"security,omitempty"`
	Examples    []any    `json:"examples,omitempty"`
	Score       float64  `json:"score,omitempty"`
}

// DocContext is a doc section in a bundle.
type DocContext struct {
	Tier      Tier   `json:"tier"`
	Service   string `json:"service"`
	Path      string `json:"path"`
	Heading   string `json:"heading"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated,omitempty"`
	URI       string `json:"uri"` // sapien://services/{name}/docs/{path}#section
}

// MemoryContext is a memory in a bundle.
type MemoryContext struct {
	Tier    Tier        `json:"tier"`
	ID      string      `json:"id"`
	Type    MemoryType  `json:"type"`
	Scope   MemoryScope `json:"scope"`
	Source  string      `json:"source"`
	Subject Subject     `json:"subject"`
	Text    string      `json:"text"`
	Score   float64     `json:"score,omitempty"`
}

// ExampleContext is a saved example in a bundle (PLAN §34b), attached to
// one of the bundle's selected operations.
type ExampleContext struct {
	ID          string         `json:"id"`
	Operation   string         `json:"operation"`
	Description string         `json:"description,omitempty"`
	Verified    bool           `json:"verified"`
	Env         string         `json:"env,omitempty"`
	Input       map[string]any `json:"input,omitempty"`
	Body        any            `json:"body,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
}

// FlowContext is a flow in a bundle.
type FlowContext struct {
	ID    string   `json:"id"`
	Name  string   `json:"name,omitempty"`
	Steps []string `json:"steps"` // "create: order-service.createOrder"
}

// RunContext is a run outcome in a bundle.
type RunContext struct {
	Tier    Tier   `json:"tier"`
	ID      string `json:"id"`
	FlowID  string `json:"flow_id,omitempty"`
	Status  string `json:"status"`
	Summary string `json:"summary"` // one line
}

// ContextBundle is what the context builder returns. Examples sits after
// Docs and before Memories, per PLAN §14/§34b's bundle order (documentation,
// then examples, then memories).
type ContextBundle struct {
	Intent          string             `json:"intent"`
	Operations      []OperationContext `json:"operations"`
	Docs            []DocContext       `json:"docs"`
	Examples        []ExampleContext   `json:"examples,omitempty"`
	Memories        []MemoryContext    `json:"memories"`
	Flows           []FlowContext      `json:"flows"`
	Runs            []RunContext       `json:"runs"`
	Omitted         map[string]int     `json:"omitted,omitempty"`
	EstimatedTokens int                `json:"estimated_tokens"`
}
