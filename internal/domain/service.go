package domain

import "time"

// SourceKind is where a service's API package comes from.
type SourceKind string

const (
	SourceLocal SourceKind = "local"
	SourceGit   SourceKind = "git"
)

// Source describes how to find a service's API package.
type Source struct {
	Kind     SourceKind `yaml:"type" json:"type"`
	Path     string     `yaml:"path,omitempty" json:"path,omitempty"`         // local: repo or package dir (may be relative to the workspace)
	URL      string     `yaml:"url,omitempty" json:"url,omitempty"`           // git
	Ref      string     `yaml:"ref,omitempty" json:"ref,omitempty"`           // git: branch, tag, or commit
	Subdir   string     `yaml:"subdir,omitempty" json:"subdir,omitempty"`     // git: package dir inside the repo (default "api")
	Contract string     `yaml:"contract,omitempty" json:"contract,omitempty"` // explicit contract file override
	// RefOverridden marks that Ref reflects this machine's local override
	// (sapien.workspace.local.yaml's `ref:`, ServiceRef.LocalRef) rather than
	// the committed workspace file (PLAN §34f item 2). Never persisted
	// (yaml/json "-"): callers set it on a copy of Source just before handing
	// it to gitsrc, whose Manager.DirFor uses it to give an overridden ref
	// its own managed-clone cache directory so two refs of one URL never
	// thrash a single clone.
	RefOverridden bool `yaml:"-" json:"-"`
}

// SyncStatus is the indexing state of a service.
type SyncStatus string

const (
	SyncPending SyncStatus = "pending"
	SyncOK      SyncStatus = "ok"
	SyncError   SyncStatus = "error"
)

// EnvHint is the non-secret, per-environment information a service may declare about itself.
type EnvHint struct {
	BaseURL string `yaml:"base_url" json:"base_url"`
}

// ServiceMetadata is the parsed contents of service.yaml. Every field is optional.
type ServiceMetadata struct {
	Version          int                `yaml:"version" json:"version"`
	Name             string             `yaml:"name,omitempty" json:"name,omitempty"`
	Description      string             `yaml:"description,omitempty" json:"description,omitempty"`
	Owners           []string           `yaml:"owners,omitempty" json:"owners,omitempty"`
	Concepts         []string           `yaml:"concepts,omitempty" json:"concepts,omitempty"`
	Tasks            []Task             `yaml:"tasks,omitempty" json:"tasks,omitempty"`
	Contracts        []string           `yaml:"contracts,omitempty" json:"contracts,omitempty"`
	Environments     map[string]EnvHint `yaml:"environments,omitempty" json:"environments,omitempty"`
	AcceptedWarnings []AcceptedWarning  `yaml:"accepted_warnings,omitempty" json:"accepted_warnings,omitempty"`
}

// AcceptedWarning is a committed, reviewable acceptance of a class of lint
// warning (service.yaml `accepted_warnings`). It exists so a warning that
// faithfully describes the wire (an endpoint that really returns
// text/plain, say) can be marked reviewed instead of driving an agent to
// "fix" it by misdescribing the contract.
//
// Code is required and must match a LintWarning.Code exactly. Match, when
// set, is matched case-insensitively as a substring against the warning's
// Message and, if present, its Source.File and Source.Pointer; when Match
// is empty every warning with Code is accepted. Reason is required
// (non-empty) and should explain why the warning is correct as-is.
type AcceptedWarning struct {
	Code   string `yaml:"code" json:"code"`
	Match  string `yaml:"match,omitempty" json:"match,omitempty"`
	Reason string `yaml:"reason" json:"reason"`
}

// Service is a registered service and its indexing state.
//
// Warnings holds only the warnings that no accepted_warnings entry in
// service.yaml matched: what a reviewer still has to look at. A matched
// warning moves to AcceptedWarnings instead, carrying the reason it was
// accepted, so `status: ok` never has to mean "no warnings were emitted",
// only "this was reviewed."
type Service struct {
	ID               string                `json:"id"` // == Name
	Name             string                `json:"name"`
	Description      string                `json:"description,omitempty"`
	Owners           []string              `json:"owners,omitempty"`
	Concepts         []string              `json:"concepts,omitempty"`
	Tasks            []Task                `json:"tasks,omitempty"`
	Source           Source                `json:"source"`
	PackageDir       string                `json:"package_dir"` // absolute path of the resolved API package directory
	ContractFiles    []string              `json:"contract_files,omitempty"`
	Environments     map[string]EnvHint    `json:"environments,omitempty"`
	Status           SyncStatus            `json:"status"`
	Error            string                `json:"error,omitempty"`
	Warnings         []LintWarning         `json:"warnings,omitempty"`
	AcceptedWarnings []AcceptedLintWarning `json:"accepted_warnings,omitempty"`
	Coverage         *DocCoverage          `json:"coverage,omitempty"`
	TaskCoverage     *TaskCoverage         `json:"task_coverage,omitempty"`
	LastIndexed      time.Time             `json:"last_indexed,omitempty"`
	Commit           string                `json:"commit,omitempty"` // git sources: resolved commit
	// Binding says which source this machine reads the service from and
	// whether service-scoped knowledge can be written into it (PLAN §7b).
	Binding        *ServiceBinding   `json:"binding,omitempty"`
	OperationCount int               `json:"operation_count"`
	WarningRules   []AcceptedWarning `json:"-" yaml:"-"`
}

// LintWarning is a non-fatal problem found while ingesting a contract.
type LintWarning struct {
	Code    string     `json:"code"`
	Message string     `json:"message"`
	Source  *SourceLoc `json:"source,omitempty"`
}

// AcceptedLintWarning is a LintWarning that a service.yaml accepted_warnings
// entry matched, carrying the reason it was accepted.
type AcceptedLintWarning struct {
	LintWarning
	Reason string `json:"reason"`
}

// DocCoverage measures how much of a service an agent in another repo can
// actually learn from Sapien, as opposed to how much of it is merely indexed.
// A contract makes operations findable; docs are what say when to call one and
// what happens if you do, and a request example is what saves the next caller
// from compiling a payload out of a schema. Counts, not opinions: the lint
// warnings name the specific operations.
type DocCoverage struct {
	Operations  int `json:"operations"`   // excluding deprecated ones
	Deprecated  int `json:"deprecated"`   // excluded from every other count here
	Documented  int `json:"documented"`   // referenced by at least one api/docs section
	WithExample int `json:"with_example"` // has a contract example or a saved example file
	NeedExample int `json:"need_example"` // takes a request body, so an example is worth having
	DocSections int `json:"doc_sections"` // narrative sections (api/docs/*.md), excluding contract-derived ones
}

// Binding modes: where this machine reads a service from.
const (
	// BindingLocal: a local checkout, either a local source in the
	// committed workspace or a per-machine override of a git source.
	BindingLocal = "local"
	// BindingTeam: the committed git source, read from the managed clone.
	BindingTeam = "team"
)

// ServiceBinding describes which source a service is read from on this
// machine ("listening to") and what it could be read from instead ("can
// listen to"). A git source is read from a managed clone that Sapien resets
// on every sync, so nothing may be written into it; binding a local
// checkout (sapien.workspace.local.yaml) makes the service writable and
// lets contributions ride the developer's own branch.
type ServiceBinding struct {
	Mode string `json:"mode"` // BindingLocal | BindingTeam
	// Team is the committed source when it is a git source: the one every
	// other machine reads. Set in both modes so the UI can show what this
	// machine would fall back to.
	Team *Source `json:"team,omitempty"`
	// Local describes the checkout being read when Mode is BindingLocal.
	Local *LocalCheckout `json:"local,omitempty"`
	// Writable reports whether service-scoped memories, examples and flows
	// can be written for this service here: true for a local checkout,
	// false for a managed clone.
	Writable bool `json:"writable"`
	// RefOverride is set when this machine reads a different ref than the
	// committed one (PLAN §34f item 2): ServiceRef.LocalRef, alongside the
	// scope it was set at. nil when this machine reads the committed ref.
	RefOverride *RefOverride `json:"ref_override,omitempty"`
}

// RefOverride is ServiceBinding.RefOverride's shape, and the scope argument
// ServiceAPI.SetRef takes (PLAN §34f item 2): "local" writes
// sapien.workspace.local.yaml only (this machine); "team" rewrites
// source.ref in the committed sapien.workspace.yaml.
type RefOverride struct {
	Ref   string `json:"ref"`
	Scope string `json:"scope"` // RefScopeLocal | RefScopeTeam
}

// Ref override scopes (PLAN §34f item 2).
const (
	RefScopeLocal = "local"
	RefScopeTeam  = "team"
)

// LocalCheckout describes a local git checkout of a service: the path this
// machine reads, and what git says about it (read-only queries; Sapien
// never fetches, checks out or commits in a developer's repository).
type LocalCheckout struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
	Remote string `json:"remote,omitempty"` // origin URL, when the path is a git repository
	// Dirty counts modified and untracked files under the package
	// directory: work that exists here and nowhere else yet.
	Dirty int `json:"dirty,omitempty"`
	// CommittedAt is HEAD's commit time: how recently this clone was
	// worked in, which is what separates the checkout someone develops in
	// from an old backup clone of the same repository.
	CommittedAt time.Time `json:"committed_at,omitzero"`
	// Ahead and Behind count commits between HEAD and the team's ref as this
	// clone last fetched it (origin/<ref>); zero when the ref is unknown here.
	Ahead  int `json:"ahead,omitempty"`
	Behind int `json:"behind,omitempty"`
	// Worktree reports a `git worktree` checkout rather than a full clone.
	Worktree bool `json:"worktree,omitempty"`
	// Package is the API package directory discovered under Path ("" when
	// none): a checkout without one cannot be indexed yet.
	Package string `json:"package,omitempty"`
}
