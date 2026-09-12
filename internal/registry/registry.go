// Package registry turns workspace service references (PLAN.md §7) into
// catalog snapshots (PLAN.md §6), watches their on-disk packages for changes
// (PLAN.md §17), and drives synchronization through the Indexer interface.
// Indexer is implemented elsewhere (internal/catalog) and adapted in; this
// package never imports the catalog package.
//
// Git-sourced services (PLAN.md §18) are not implemented yet: Builder.Build
// returns an errs.NotImplemented error for them. That is intentionally out of
// scope for this package.
package registry

import (
	"context"

	"github.com/gs-sinha/sapien/internal/domain"
)

// Snapshot is the normalized, ready-to-index view of one service, mirroring
// internal/catalog.Snapshot field-for-field. The engine wiring converts
// between the two with a plain struct conversion.
type Snapshot struct {
	Service       domain.Service
	Operations    []domain.Operation
	Schemas       []domain.NamedSchema
	Fields        []domain.Field
	Aliases       []domain.Alias
	Docs          []domain.Doc
	Flows         []domain.FlowSummary
	Tasks         []domain.Task
	ContractFiles map[string]string // relative path -> sha256 hex digest
}

// Indexer applies snapshots (or sync failures) to the persisted catalog. The
// engine wiring supplies an implementation backed by internal/catalog.
type Indexer interface {
	Apply(ctx context.Context, snap Snapshot) (domain.CatalogChange, error)
	MarkServiceError(ctx context.Context, svc domain.Service, msg string) error
	RemoveService(ctx context.Context, id string) error
}

// TaskReviewer is an optional second phase implemented by an indexer that
// can run held-out task assertions through the production search path after
// Apply has made the new task index visible.
type TaskReviewer interface {
	ReviewTasks(ctx context.Context, snap Snapshot) (domain.Service, error)
}

// AcceptWarning applies one service.yaml warning rule set to a warning.
func AcceptWarning(w domain.LintWarning, rules []domain.AcceptedWarning) (domain.AcceptedLintWarning, bool) {
	reason, ok := firstMatchingReason(w, rules)
	return domain.AcceptedLintWarning{LintWarning: w, Reason: reason}, ok
}

// WarningMatches reports whether a service.yaml acceptance rule matches a warning.
func WarningMatches(w domain.LintWarning, rule domain.AcceptedWarning) bool {
	return warningMatchesRule(w, rule)
}
