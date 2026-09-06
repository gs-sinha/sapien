package memory

import "context"

// OperationInfo is the catalog information Store needs about an operation to
// resolve a memory subject and to expand structural retrieval (PLAN.md §13,
// §14). It is a small, memory-package-local projection of domain.Operation so
// this package does not need to depend on internal/catalog.
type OperationInfo struct {
	Service string
	Method  string
	Path    string
	Hash    string
	Schemas []string // component schema names used by the operation's request/response bodies
	Tags    []string
}

// Resolver is the catalog lookup surface Store needs. Production wiring
// backs it with the catalog package; tests use a fake. A nil Resolver is
// valid: Store simply never resolves subjects or expands structural
// candidates beyond what's already recorded on the memory itself.
type Resolver interface {
	// Operation looks up an operation by ID. ok is false if the ID is not
	// known to the catalog (e.g. the contract changed since the memory was
	// saved).
	Operation(ctx context.Context, id string) (info OperationInfo, ok bool)
	// FlowsUsing returns the IDs of flows that call operationID.
	FlowsUsing(ctx context.Context, operationID string) []string
	// FieldExists reports whether fieldPath is a known field of operationID.
	FieldExists(ctx context.Context, operationID, fieldPath string) bool
}
