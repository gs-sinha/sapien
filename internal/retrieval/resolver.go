package retrieval

import (
	"context"

	"github.com/gs-sinha/sapien/internal/catalog"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/memory"
)

// CatalogResolver adapts a *catalog.Catalog to memory.Resolver. The engine
// wiring constructs one of these and hands it to memory.New so the memory
// store can resolve subjects and expand structural retrieval (PLAN.md §13)
// against the real catalog.
type CatalogResolver struct {
	Cat *catalog.Catalog
}

// Operation looks up id in the catalog and projects it to the small
// memory.OperationInfo shape. ok is false when the catalog doesn't know id.
//
// Schemas lists every component schema (as "<service>.<ComponentName>", the
// format memory subjects use, PLAN.md §11) reached directly or nested while
// building op's params/request body/responses: catalog.ListSchemas already
// records exactly that set per schema as NamedSchema.UsedBy, so Schemas here
// is simply every schema whose UsedBy contains op.ID.
//
// Tags is the union of op.Tags (OpenAPI tags) and op.Concepts
// (service.yaml concepts): both are free-form categorization strings a
// memory's tags might overlap with (PLAN.md §13's "concept/tag overlap"
// tier), and only Concepts carries values like "qcom" that a fixture
// service actually declares.
func (r *CatalogResolver) Operation(ctx context.Context, id string) (memory.OperationInfo, bool) {
	op, err := r.Cat.GetOperation(ctx, id)
	if err != nil {
		return memory.OperationInfo{}, false
	}

	info := memory.OperationInfo{
		Service: op.ServiceID,
		Hash:    op.Hash,
		Tags:    unionStrings(op.Tags, op.Concepts),
	}
	if op.HTTP != nil {
		info.Method = op.HTTP.Method
		info.Path = op.HTTP.Path
	}

	schemas, err := schemasForOperation(ctx, r.Cat, *op)
	if err == nil {
		for _, s := range schemas {
			info.Schemas = append(info.Schemas, s.ServiceID+"."+s.Name)
		}
	}
	return info, true
}

// FlowsUsing returns the IDs of every flow that calls operationID.
func (r *CatalogResolver) FlowsUsing(ctx context.Context, operationID string) []string {
	flows, err := r.Cat.FlowsUsingOperation(ctx, operationID)
	if err != nil {
		return nil
	}
	ids := make([]string, len(flows))
	for i, f := range flows {
		ids[i] = f.ID
	}
	return ids
}

// FieldExists reports whether fieldPath is one of operationID's known
// fields.
func (r *CatalogResolver) FieldExists(ctx context.Context, operationID, fieldPath string) bool {
	fields, err := r.Cat.Fields(ctx, operationID)
	if err != nil {
		return false
	}
	for _, f := range fields {
		if f.Path == fieldPath {
			return true
		}
	}
	return false
}

// schemasForOperation returns the component schemas of op.ServiceID whose
// UsedBy includes op.ID (PLAN.md §5/§15: NamedSchema.UsedBy is computed by
// the ingest builder for every component reached, directly or nested,
// while normalizing an operation's params/request body/responses).
func schemasForOperation(ctx context.Context, cat *catalog.Catalog, op domain.Operation) ([]domain.NamedSchema, error) {
	all, err := cat.ListSchemas(ctx, op.ServiceID)
	if err != nil {
		return nil, err
	}
	var out []domain.NamedSchema
	for _, s := range all {
		for _, used := range s.UsedBy {
			if used == op.ID {
				out = append(out, s)
				break
			}
		}
	}
	return out, nil
}

// unionStrings returns the distinct values across every list, in first-seen
// order.
func unionStrings(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range lists {
		for _, v := range list {
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
