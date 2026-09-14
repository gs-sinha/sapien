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

	// Bind makes this machine read name from the local checkout at path
	// instead of its committed git source, recording the override in
	// sapien.workspace.local.yaml (never in the committed file), and
	// resyncs the service from there. Service-scoped knowledge becomes
	// writable and rides the developer's own branch.
	Bind(ctx context.Context, name, path string) (*domain.Service, error)
	// Unbind removes the override so the service is read from its
	// committed source again, and resyncs it.
	Unbind(ctx context.Context, name string) (*domain.Service, error)
	// Binding reports what the service is read from here, what it could
	// be read from instead, and any local checkouts of the same remote
	// this machine already knows about.
	Binding(ctx context.Context, name string) (*BindingInfo, error)

	// BindWith is Bind with options. An empty name infers the service from
	// the checkout's origin (the one team source naming that repository;
	// ambiguity is an error). Without Force, a checkout whose origin names
	// another repository, or which has no API package, is refused.
	BindWith(ctx context.Context, name, path string, opts BindOptions) (*domain.Service, error)
	// BrowseCheckouts lists the subdirectories of dir (the home directory
	// when dir is "") for a picker, describing each git repository found
	// and saying whether it is a checkout of name's team repository.
	BrowseCheckouts(ctx context.Context, name, dir string) (*DirListing, error)
	// AddFromCheckout registers the repository a local checkout was cloned
	// from as a git source in the committed workspace file, and binds the
	// checkout on this machine, so the team gets a source every clone can
	// read while this machine reads the working copy at once. name ""
	// derives the name from the contract, as Add does.
	AddFromCheckout(ctx context.Context, name, path string, opts AddFromCheckoutOptions) (*domain.Service, error)
}

// BindingInfo is Services().Binding's answer: the service's current binding
// plus local checkouts of the same repository found on this machine, so a
// UI can offer "read from ~/code/rider-service instead" as one click.
type BindingInfo struct {
	Service    string                 `json:"service"`
	Binding    domain.ServiceBinding  `json:"binding"`
	Candidates []domain.LocalCheckout `json:"candidates,omitempty"`
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
	// Create writes a new flow file into the workspace tier (<workspace>/flows)
	// after validation. Shorthand for CreateIn with OwnerKind workspace.
	Create(ctx context.Context, yamlSrc string, path string) (*domain.Flow, error)
	// CreateIn writes a new flow file into the tier opts names after
	// validation: local (this machine, the default), workspace (the team's
	// repo), or service (the owning service's api/flows, only when that
	// service is bound to a writable checkout).
	CreateIn(ctx context.Context, yamlSrc string, opts CreateFlowOptions) (*domain.Flow, error)
	// Rescope moves an existing flow to another tier, keeping its file name,
	// and reindexes both owners. ownerID names the service for ownerKind
	// service and is ignored otherwise.
	Rescope(ctx context.Context, id string, ownerKind, ownerID string) (*domain.Flow, error)
	// RescopeWith is Rescope with options: Commit records the moved file in
	// the workspace repository with one commit (never a push, never a
	// service repository), only when the target tier is workspace.
	RescopeWith(ctx context.Context, id string, ownerKind, ownerID string, opts RescopeOptions) (*domain.Flow, error)
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

// CreateFlowOptions says where FlowAPI.CreateIn saves a new flow.
type CreateFlowOptions struct {
	// Path is the destination relative to the chosen tier's flows
	// directory; default "<id>.flow.yaml" from the flow's own id.
	Path string
	// OwnerKind is the tier: domain.FlowOwnerLocal (default when empty),
	// domain.FlowOwnerWorkspace, or domain.FlowOwnerService.
	OwnerKind string
	// OwnerID names the service for OwnerKind service; ignored otherwise.
	OwnerID string
}

// BindOptions tunes ServiceAPI.BindWith.
type BindOptions struct {
	// Force binds a checkout whose origin does not match the team source
	// (a fork, a mirror) or which has no API package yet.
	Force bool
}

// AddFromCheckoutOptions tunes ServiceAPI.AddFromCheckout.
type AddFromCheckoutOptions struct {
	// Ref is the branch the team source pins; "" means the remote's
	// default branch, resolved when the clone is first made.
	Ref string
	// Force registers even when the checkout has no API package yet.
	Force bool
	// AllowSubdir accepts a path that is a subdirectory of its repository
	// (a monorepo): the team source then carries Subdir, the package's path
	// inside the repository. Without it such a path is refused with an
	// errs.Invalid carrying the detail local_add=true, meaning a plain local
	// Add is the sensible fallback; the same detail marks a path that is not
	// a git checkout with an origin at all.
	AllowSubdir bool
}

// DirListing is ServiceAPI.BrowseCheckouts' answer: one directory level,
// for a picker that walks the filesystem from the daemon's side of the
// browser boundary (a web page cannot learn an absolute path from a file
// dialog).
type DirListing struct {
	Path    string     `json:"path"`
	Parent  string     `json:"parent,omitempty"` // "" at the filesystem root
	Entries []DirEntry `json:"entries"`
}

// DirEntry is one subdirectory in a DirListing. Checkout is set when the
// directory is the root of a git repository; Matches says its origin names
// the service's team repository, and Reason says why it does not when it
// is a repository of something else or lacks an API package.
type DirEntry struct {
	Name     string                `json:"name"`
	Path     string                `json:"path"`
	Checkout *domain.LocalCheckout `json:"checkout,omitempty"`
	Matches  bool                  `json:"matches"`
	Reason   string                `json:"reason,omitempty"`
}

// RescopeOptions tunes FlowAPI.RescopeWith.
type RescopeOptions struct {
	// Commit runs `git add` and `git commit` for the moved file in the
	// workspace repository after a promotion to the workspace tier. Opt-in:
	// the default leaves the file for the human to commit, and Sapien
	// never pushes. Refused (errs.Invalid) when the target tier is not
	// workspace or the workspace is not inside a git repository.
	Commit bool
	// Message overrides the default commit message.
	Message string
}
