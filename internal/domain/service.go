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
	Source           Source                `json:"source"`
	PackageDir       string                `json:"package_dir"` // absolute path of the resolved API package directory
	ContractFiles    []string              `json:"contract_files,omitempty"`
	Environments     map[string]EnvHint    `json:"environments,omitempty"`
	Status           SyncStatus            `json:"status"`
	Error            string                `json:"error,omitempty"`
	Warnings         []LintWarning         `json:"warnings,omitempty"`
	AcceptedWarnings []AcceptedLintWarning `json:"accepted_warnings,omitempty"`
	LastIndexed      time.Time             `json:"last_indexed,omitempty"`
	Commit           string                `json:"commit,omitempty"` // git sources: resolved commit
	OperationCount   int                   `json:"operation_count"`
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
