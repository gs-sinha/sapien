package catalog

import "github.com/growsimplee/sapien/internal/domain"

// Snapshot is the fully-ingested, normalized representation of one service,
// as produced by the registry from an ingest run. Apply upserts it into the
// catalog atomically, diffing operations against what is already stored.
type Snapshot struct {
	// Service.ID must equal Service.Name. Status/Error/Warnings/LastIndexed
	// may be set by the caller but are overwritten by Apply (status "ok",
	// error cleared, last_indexed = now); MarkServiceError is the path for
	// recording a failed ingest instead of calling Apply.
	Service domain.Service

	Operations []domain.Operation
	Schemas    []domain.NamedSchema
	Fields     []domain.Field // Fields[i].OperationID selects which Operations row it belongs to
	Aliases    []domain.Alias // Aliases[i].OperationID selects which Operations row it belongs to

	Docs []domain.Doc // Sections and Refs populated

	// Flows are the service-owned flows (owner_kind "service", owner_id ==
	// Service.ID). Workspace-owned flows are managed separately via
	// Catalog.UpsertFlows("workspace", "", flows).
	Flows []domain.FlowSummary

	// ContractFiles maps a contract file's path (relative to the service
	// package) to its content hash.
	ContractFiles map[string]string
}
