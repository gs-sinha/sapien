// Package engine defines the Engine facade that every surface (CLI, HTTP, MCP)
// talks to. There are two implementations (PLAN §4): Local runs in-process;
// Remote is an HTTP client to a running daemon. Commands are written once
// against this interface and never branch on which one they got.
package engine

import (
	"context"
	"io"

	"github.com/gs-sinha/sapien/internal/domain"
)

// Engine is the facade.
type Engine interface {
	Workspace() *domain.Workspace
	Services() ServiceAPI
	Catalog() CatalogAPI
	Search() SearchAPI
	Flows() FlowAPI
	Runner() RunnerAPI
	Runs() RunAPI
	Memories() MemoryAPI
	Examples() ExampleAPI
	Context() ContextAPI
	Envs() EnvAPI
	Events() EventAPI
	// Close releases resources. Local closes the DB; Remote closes connections.
	Close() error
}

// ServiceAPI manages registered services and their synchronization.
type ServiceAPI interface {
	List(ctx context.Context) ([]domain.Service, error)
	Get(ctx context.Context, name string) (*domain.Service, error)
	// Add registers a source, writes it to sapien.workspace.yaml, and indexes it.
	Add(ctx context.Context, name string, src domain.Source) (*domain.Service, error)
	Remove(ctx context.Context, name string) error
	// Sync re-reads the source (git fetch for managed clones) and reindexes. Empty name = all.
	Sync(ctx context.Context, name string) ([]domain.Service, error)
	// Reindex rebuilds the catalog for all services from canonical files.
	Reindex(ctx context.Context) error
}

// CatalogAPI reads the normalized catalog.
type CatalogAPI interface {
	GetOperation(ctx context.Context, id string) (*domain.Operation, error)
	// ResolveOperation accepts an ID, "METHOD /path", or a bare operationId and returns the operation.
	ResolveOperation(ctx context.Context, ref string) (*domain.Operation, error)
	ListOperations(ctx context.Context, service string) ([]domain.Operation, error)
	Fields(ctx context.Context, operationID string) ([]domain.Field, error)
	GetSchema(ctx context.Context, service, name string) (*domain.NamedSchema, error)
	ListDocs(ctx context.Context, service string) ([]domain.Doc, error)
	GetDoc(ctx context.Context, service, path string) (*domain.Doc, error)
}

// SearchAPI is lexical (and optionally semantic) search.
type SearchAPI interface {
	Operations(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.SearchResult, error)
	Docs(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.DocSearchResult, error)
}

// FlowAPI manages flow files.
type FlowAPI interface {
	List(ctx context.Context, query string) ([]domain.FlowSummary, error)
	Get(ctx context.Context, id string) (*domain.Flow, error)
	// Parse turns YAML into a Flow without validating references.
	Parse(ctx context.Context, yamlSrc string) (*domain.Flow, error)
	// Validate checks references, bindings, and expressions against the catalog.
	Validate(ctx context.Context, yamlSrc string) (*domain.ValidationResult, error)
	// Create writes a new flow file (workspace flows dir by default) after validation.
	Create(ctx context.Context, yamlSrc string, path string) (*domain.Flow, error)
	Update(ctx context.Context, id string, yamlSrc string) (*domain.Flow, error)
	Delete(ctx context.Context, id string) error
	// Reference returns the DSL reference text for agents (PLAN §23): topic is sapien|flow|memory|expressions|service.
	Reference(ctx context.Context, topic string) (string, error)
}

// RunOptions control an execution.
type RunOptions struct {
	Environment       string
	Inputs            map[string]any
	ContinueOnFailure bool
	AllowProduction   bool
	Trigger           string // cli | ui | mcp | ci
	// Observer, when set, receives step updates as they happen.
	Observer func(domain.Event)

	// ResumeFrom names an earlier run of the same flow whose step results
	// are reused instead of executed: every setup step and every step before
	// FromStep (default: the first step that did not pass in that run) is
	// copied with its request, response, and extracted values, so later
	// expressions resolve exactly as before. A reused step whose definition
	// changed since that run is still reused, with a warning on the step.
	ResumeFrom string
	// FromStep is the first step to execute (inclusive). With ResumeFrom it
	// overrides the default resume point; without it, earlier steps are
	// skipped and their references are unavailable.
	FromStep string
	// UntilStep is the last step to execute (inclusive); later steps are
	// skipped. Teardown still runs.
	UntilStep string
}

// CallRequest executes a single operation as a one-step run.
type CallRequest struct {
	Operation       string
	Params          map[string]any // path/query/header by name
	Body            any
	Headers         map[string]string
	Env             string
	AllowProduction bool
	Trigger         string
}

// RunnerAPI executes flows and single calls.
type RunnerAPI interface {
	RunFlow(ctx context.Context, flowID string, opts RunOptions) (*domain.Run, error)
	// RunFlowSource executes an unsaved flow.
	RunFlowSource(ctx context.Context, yamlSrc string, opts RunOptions) (*domain.Run, error)
	Call(ctx context.Context, req CallRequest) (*domain.Run, error)
	Cancel(ctx context.Context, runID string) error
}

// RunAPI reads run history.
type RunAPI interface {
	List(ctx context.Context, filter domain.RunFilter) ([]domain.Run, error) // Steps omitted
	Get(ctx context.Context, id string) (*domain.Run, error)                 // Steps included
	Pin(ctx context.Context, id string, pinned bool) error
	Purge(ctx context.Context, keep int) (int, error)
}

// MemoryAPI manages memories.
type MemoryAPI interface {
	Create(ctx context.Context, m domain.Memory) (*domain.Memory, error)
	Get(ctx context.Context, id string) (*domain.Memory, error)
	Update(ctx context.Context, m domain.Memory) (*domain.Memory, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, q domain.MemoryQuery) ([]domain.Memory, error)
	Search(ctx context.Context, q domain.MemoryQuery) ([]domain.ScoredMemory, error)
	Relevant(ctx context.Context, subjects []domain.Subject, limit int) ([]domain.ScoredMemory, error)
	// PromotionTarget locates where a memory should be promoted (PLAN §26).
	PromotionTarget(ctx context.Context, id string) (*PromotionTarget, error)
	Reindex(ctx context.Context) error
}

// PromotionTarget is where a memory's knowledge belongs canonically.
type PromotionTarget struct {
	Kind      string        `json:"kind"` // "openapi" | "doc"
	File      string        `json:"file"`
	Line      int           `json:"line,omitempty"`
	Pointer   string        `json:"pointer,omitempty"`
	Section   string        `json:"section,omitempty"`
	Current   string        `json:"current,omitempty"` // current description / section body
	Memory    domain.Memory `json:"memory"`
	Suggested string        `json:"suggested,omitempty"` // template-only suggestion
}

// ExampleAPI manages saved, verified request examples (PLAN §34b): one
// operation each, stored as files at workspace or service scope, indexed
// in SQLite, replayable by the CLI, flows, MCP hosts, and a UI.
type ExampleAPI interface {
	List(ctx context.Context, q domain.ExampleQuery) ([]domain.SavedExample, error)
	Get(ctx context.Context, id string) (*domain.SavedExample, error)
	// Create validates the example against the catalog (the operation must
	// exist; input names and body shape are checked like a flow step) and
	// writes its file. Verified must be nil: only FromRun sets it.
	Create(ctx context.Context, ex domain.SavedExample) (*domain.SavedExample, error)
	// Update rewrites the example; a changed Scope moves the file.
	Update(ctx context.Context, ex domain.SavedExample) (*domain.SavedExample, error)
	Delete(ctx context.Context, id string) error
	// FromRun saves one step of a recorded run as a verified example: the
	// request as sent (body, non-auth headers, path/query inputs recovered
	// from the URL) and the observed response as Expect.
	FromRun(ctx context.Context, req ExampleFromRun) (*domain.SavedExample, error)
	// ForOperations returns the examples of the given operations, verified
	// first, for the context builder and get_api.
	ForOperations(ctx context.Context, operationIDs []string, limit int) ([]domain.SavedExample, error)
	Reindex(ctx context.Context) error
}

// ExampleFromRun names the run step to save and how to file it.
type ExampleFromRun struct {
	RunID       string               `json:"run_id"`
	StepID      string               `json:"step_id,omitempty"` // default: the run's only (or first) step
	ID          string               `json:"id"`                // example id (file stem)
	Description string               `json:"description,omitempty"`
	Scope       domain.ExampleScope  `json:"scope,omitempty"` // default workspace
	Tags        []string             `json:"tags,omitempty"`
	Source      *domain.MemorySource `json:"source,omitempty"` // who saved it: user | agent{client}
}

// ContextAPI builds agent context bundles.
type ContextAPI interface {
	Build(ctx context.Context, req domain.ContextRequest) (*domain.ContextBundle, error)
}

// EnvAPI manages environments and secrets.
type EnvAPI interface {
	List(ctx context.Context) ([]domain.Environment, error)
	Get(ctx context.Context, name string) (*domain.Environment, error)
	Default(ctx context.Context) (string, error)
	SetDefault(ctx context.Context, name string) error
	// Secrets: values are never returned by any API.
	SetSecret(ctx context.Context, name, value string) error
	ListSecrets(ctx context.Context) ([]string, error)
	DeleteSecret(ctx context.Context, name string) error
}

// EventAPI streams engine events.
type EventAPI interface {
	// Subscribe returns a channel of events and a cancel function. The channel closes on cancel.
	Subscribe(ctx context.Context) (<-chan domain.Event, func())
}

// Closer is implemented by engines that hold resources.
var _ io.Closer = (Engine)(nil)
