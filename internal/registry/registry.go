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

	"github.com/growsimplee/sapien/internal/domain"
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
	ContractFiles map[string]string // relative path -> sha256 hex digest
}

// Indexer applies snapshots (or sync failures) to the persisted catalog. The
// engine wiring supplies an implementation backed by internal/catalog.
type Indexer interface {
	Apply(ctx context.Context, snap Snapshot) (domain.CatalogChange, error)
	MarkServiceError(ctx context.Context, svc domain.Service, msg string) error
	RemoveService(ctx context.Context, id string) error
}
