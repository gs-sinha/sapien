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
	// Repo is the workspace's own git repository: fetched on the git tick,
	// pulled only on request and only fast-forward on a clean tree.
	Repo() RepoAPI
	// Settings exposes daemon/workspace settings beyond the engine's own
	// config -- currently semantic search (PLAN §34f item 5).
	Settings() SettingsAPI
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

	// SetRef switches name's ref (PLAN §34f item 2): scope domain.RefScopeLocal
	// (default) writes only sapien.workspace.local.yaml (this machine);
	// domain.RefScopeTeam rewrites source.ref in the committed
	// sapien.workspace.yaml. Refused (errs.Invalid) when name is not
	// git-sourced, or when ref does not exist on the remote (checked via
	// ls-remote before anything is written; the error names close matches
	// when Branches was cheap to compute). Resyncs through the normal Sync
	// path afterward (fetch, move the managed clone if the ref carries a
	// new local override, reindex).
	SetRef(ctx context.Context, name, ref string, scope string) (*domain.Service, error)
	// ClearRef removes this machine's local ref override, resyncing back to
	// whatever ref is now effective (the committed one, unless a path
	// override is also active). Refused (errs.Invalid) when there is no
	// local ref override to clear.
	ClearRef(ctx context.Context, name string) (*domain.Service, error)
	// Branches lists a git-sourced service's branches and tags from
	// `ls-remote`, current naming the ref this machine actually reads
	// (Source.Ref, "" meaning the remote's default branch) and default
	// naming the remote's default branch.
	Branches(ctx context.Context, name string) (*BranchList, error)
}

// BranchList is ServiceAPI.Branches' answer (PLAN §34f item 2).
type BranchList struct {
	Current  string   `json:"current"`
	Default  string   `json:"default"`
	Branches []string `json:"branches"`
	Tags     []string `json:"tags"`
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
	// Commit records a workspace-tier flow's file in the workspace repository
	// with one commit of that file (message "" picks a default), never a push.
	// Refused (errs.Invalid) for the local and service tiers, for a workspace
	// not inside a git repository, and when the file has nothing to commit.
	// The returned summary carries the new Shipped state.
	Commit(ctx context.Context, id, message string) (*domain.FlowSummary, error)
	// Move places the flow's file at folder within its current tier's flows
	// directory (PLAN §34f item 6): keeps tier and file name; refused for a
	// read-only service tier, or (errs.Conflict) when the destination file
	// already exists; a no-op success when folder is where the flow already
	// is. See RescopeWith for the tier-only move, which itself keeps folder.
	Move(ctx context.Context, id, folder string) (*domain.Flow, error)
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
	// Move places a workspace-scope memory's file in another tier
	// (domain.TierLocal or TierWorkspace), keeping its id and scope: the
	// local tier is this machine's, the workspace tier is the team's repo.
	// Refused for personal and service scope, whose home is their scope.
	Move(ctx context.Context, id, tier string) (*domain.Memory, error)
	// MoveFolder places the memory's file at folder within its current
	// directory (PLAN §34f item 6): keeps scope and tier; refused for
	// personal scope (no file) and a read-only service, or (errs.Conflict)
	// when the destination file already exists; a no-op success when folder
	// is where the memory already is.
	MoveFolder(ctx context.Context, id, folder string) (*domain.Memory, error)
	// Commit records a workspace-tier memory's file in the workspace
	// repository with one commit of that file (message "" picks a default),
	// never a push; refused for other tiers, a workspace outside git, or a
	// file with nothing to commit. The returned memory carries Shipped.
	Commit(ctx context.Context, id, message string) (*domain.Memory, error)
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
	// Move places a workspace-scope example's file in another tier
	// (domain.TierLocal or TierWorkspace), keeping its id and scope.
	// Refused for service scope.
	Move(ctx context.Context, id, tier string) (*domain.SavedExample, error)
	// MoveFolder places the example's file at folder within its current
	// directory (PLAN §34f item 6): keeps scope and tier; refused for a
	// read-only service, or (errs.Conflict) when the destination file
	// already exists; a no-op success when folder is where the example
	// already is.
	MoveFolder(ctx context.Context, id, folder string) (*domain.SavedExample, error)
	// Commit records a workspace-tier example's file in the workspace
	// repository with one commit of that file, never a push; refused for
	// other tiers, a workspace outside git, or nothing to commit.
	Commit(ctx context.Context, id, message string) (*domain.SavedExample, error)
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
	// Iteration selects which execution of StepID to save when it names a
	// loop block's nested step, which may have run more than once (PLAN
	// §34f.8); nil defaults to its LATEST execution. Ignored when StepID
	// does not name a nested step.
	Iteration *int `json:"iteration,omitempty"`
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
	// directory; default "<id>.flow.yaml" from the flow's own id. Mutually
	// exclusive with Folder.
	Path string
	// Folder places the flow at <folder>/<id>.flow.yaml within the chosen
	// tier's flows directory (PLAN §34f item 6); "" is the root. Mutually
	// exclusive with Path.
	Folder string
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

// RepoAPI reads and, on request, updates the workspace's own git
// repository -- the team's shared copy of the workspace tier (PLAN §7b).
// Nothing here ever pushes, commits, or touches a service repository.
type RepoAPI interface {
	// Status reports the repository from refs on disk: no network.
	Status(ctx context.Context) (*domain.RepoStatus, error)
	// Fetch runs `git fetch` and returns the refreshed status. The
	// working tree is not changed. The daemon calls this on its git tick.
	Fetch(ctx context.Context) (*domain.RepoStatus, error)
	// Pull fast-forwards the checkout onto its upstream. It refuses
	// (errs.Conflict) when the tree has uncommitted changes, the branch
	// has no upstream, or the branches have diverged, naming the reason;
	// after a pull the workspace tier is reindexed.
	Pull(ctx context.Context) (*domain.RepoStatus, error)
	// Sync is what "Sync all" does for the repository: Fetch, then Pull
	// when the tree is clean and the branch is behind, otherwise a status
	// whose Skipped says why nothing moved. It never fails because a pull
	// was not possible; only a fetch or git failure is an error.
	Sync(ctx context.Context) (*domain.RepoStatus, error)
	// Push sends the branch's unpushed commits to its upstream (setting the
	// upstream to origin/<branch> when the branch has none). Only on
	// request, only the workspace repository, never a force push, never a
	// service repository; refused (errs.Conflict) when the branch is behind,
	// since a pull must come first, and a no-op success when nothing is
	// ahead. The returned status carries Pushed and PushedCount.
	Push(ctx context.Context) (*domain.RepoStatus, error)

	// Changes lists every changed file the workspace repository knows about
	// (PLAN §34f item 1): the source-control-panel view behind the Changes
	// page. Every workspace-tier file (flows, memories, examples,
	// environments, sapien.workspace.yaml, .gitignore) is covered, classified
	// into a kind/id/title where the catalog recognizes the path; bound
	// local service checkouts are listed too (branch, changed files under
	// the API package), read-only.
	Changes(ctx context.Context) (*domain.RepoChanges, error)
	// Diff reports one file's diff or (for an untracked file) content.
	// path is repo-root-relative, as Changes reports it.
	Diff(ctx context.Context, path string) (*domain.RepoDiff, error)
	// Commit stages and commits exactly paths (repo-root-relative) in the
	// workspace repository with one commit, never pushes, never amends,
	// never passes --no-verify. Refused (errs.Invalid) when message is
	// empty, when paths is empty, or when nothing in paths has a change to
	// commit. Emits EventWorkspaceRepo so the status bar refreshes.
	Commit(ctx context.Context, paths []string, message string) (*domain.RepoCommitResult, error)
}

// SettingsAPI manages daemon/workspace-level settings exposed to a UI or
// CLI beyond the engine's own internal/config (PLAN §34f item 5): today
// just semantic search. A future §34f slice (daemon control, updates) adds
// more methods here the same way.
type SettingsAPI interface {
	// GetSemantic reports the effective, merged semantic-search
	// configuration (workspace overriding user; never the api_key itself)
	// plus its live status.
	GetSemantic(ctx context.Context) (*domain.SemanticSettings, error)
	// PutSemantic validates req (defaulting base_url/batch_size, requiring
	// kind when enabled), probes the provider the way TestSemantic does
	// (unless req.Force), writes it to the file req.Scope names ("user",
	// the default, or "workspace"), and applies it live to this
	// workspace -- swapping the embedder under a lock and triggering a
	// reindex when it was just turned on or kind/base_url/model changed. A
	// daemon serving several workspaces additionally reapplies a
	// "user"-scope change to every other open one (internal/server, since
	// only it can see every open engine).
	PutSemantic(ctx context.Context, req SemanticPutRequest) (*domain.SemanticSettings, error)
	// TestSemantic probes req without saving it: one short embed call.
	// Reported through SemanticTestResult.OK/Error rather than a Go error,
	// whether the provider accepts it or not -- an error return is
	// reserved for something unexpected.
	TestSemantic(ctx context.Context, req domain.SemanticProbe) (*domain.SemanticTestResult, error)
	// ReindexSemantic starts a full rebuild of the semantic vector index in
	// the background (semantic.index events report progress) and returns
	// once it has started, not once it has finished.
	ReindexSemantic(ctx context.Context) error
	// OllamaStatus probes an Ollama endpoint's /api/tags. baseURL ""
	// defers to the workspace's configured semantic.base_url, defaulting
	// to http://127.0.0.1:11434.
	OllamaStatus(ctx context.Context, baseURL string) (*domain.OllamaStatus, error)
	// OllamaPull starts `ollama pull` for req.Model in the background
	// (semantic.pull events report its NDJSON progress) and returns once
	// it has started; errs.Conflict when that model is already being
	// pulled.
	OllamaPull(ctx context.Context, req domain.OllamaPullRequest) error
}

// SemanticPutRequest is PUT /v1/settings/semantic's request body.
type SemanticPutRequest struct {
	Enabled   bool   `json:"enabled"`
	Kind      string `json:"kind,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	Model     string `json:"model,omitempty"`
	BatchSize int    `json:"batch_size,omitempty"`
	// APIKey is tri-state: nil (the field absent from the JSON body) keeps
	// the scope's stored key, a pointer to "" clears it, anything else
	// (including a verbatim "${env.NAME}" reference) sets it.
	APIKey *string `json:"api_key,omitempty"`
	// Kinds is what to embed (config.SemanticKinds); absent keeps the
	// stored list, an empty list means every kind.
	Kinds *[]string `json:"kinds,omitempty"`
	// QueryPrefix/DocumentPrefix override the model's own task prefixes
	// ("" is a valid override: none); absent keeps what is stored.
	// ResetPrefixes drops both overrides so the prefixes follow the model
	// again, and wins over the two fields.
	QueryPrefix    *string `json:"query_prefix,omitempty"`
	DocumentPrefix *string `json:"document_prefix,omitempty"`
	ResetPrefixes  bool    `json:"reset_prefixes,omitempty"`
	// KeepAlive is Ollama's keep_alive ("30s", "5m", "0"); absent keeps
	// what is stored, "" goes back to Ollama's own default.
	KeepAlive *string `json:"keep_alive,omitempty"`
	// Scope is "user" (the default, when empty) or "workspace".
	Scope string `json:"scope,omitempty"`
	// Force saves an enabled config even when the pre-save probe (the same
	// one TestSemantic runs) fails.
	Force bool `json:"force,omitempty"`
}
