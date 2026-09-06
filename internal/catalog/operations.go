package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/store"
	"github.com/gs-sinha/sapien/internal/textutil"
)

// GetOperation returns the operation with the given full ID
// ("<service>.<operationId>"), or errs.OperationNotFound with
// Details["suggestions"] set to the nearest IDs.
func (c *Catalog) GetOperation(ctx context.Context, id string) (*domain.Operation, error) {
	var docJSON string
	err := c.db.SQL().QueryRowContext(ctx, `SELECT doc_json FROM operations WHERE id = ?`, id).Scan(&docJSON)
	if errors.Is(err, sql.ErrNoRows) {
		suggestions, _ := c.SuggestOperationIDs(ctx, id, 5)
		return nil, errs.New(errs.OperationNotFound, "operation %q not found", id).WithDetail("suggestions", suggestions)
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get operation %q: %w", id, err)
	}
	var op domain.Operation
	if err := store.UnmarshalJSON(docJSON, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// ListOperations returns every operation for service (or every operation,
// if service is ""), ordered by service, path, method.
func (c *Catalog) ListOperations(ctx context.Context, service string) ([]domain.Operation, error) {
	query := `SELECT doc_json FROM operations`
	var args []any
	if service != "" {
		query += ` WHERE service_id = ?`
		args = append(args, service)
	}
	query += ` ORDER BY service_id, path, method`

	rows, err := c.db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog: list operations: %w", err)
	}
	defer rows.Close()

	var out []domain.Operation
	for rows.Next() {
		var docJSON string
		if err := rows.Scan(&docJSON); err != nil {
			return nil, fmt.Errorf("catalog: scan operation: %w", err)
		}
		var op domain.Operation
		if err := store.UnmarshalJSON(docJSON, &op); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate operations: %w", err)
	}
	return out, nil
}

// ResolveOperation accepts one of four reference shapes:
//
//   - a full operation ID, "<service>.<operationId>"
//   - "METHOD /path": an exact operation_aliases match, else a templated
//     match (textutil.PathMatches) over aliases with that method
//   - a bare "/path" (any method): errs.Conflict if more than one operation matches
//   - a bare operationId, e.g. "getRider" (case-insensitive; must be unique
//     across every service, else errs.Conflict with Details["candidates"])
func (c *Catalog) ResolveOperation(ctx context.Context, ref string) (*domain.Operation, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errs.New(errs.Invalid, "empty operation reference")
	}

	if method, path, ok := textutil.ParseURLQuery(ref); ok {
		if method != "" {
			return c.resolveMethodPath(ctx, method, path, ref)
		}
		return c.resolveBarePath(ctx, path, ref)
	}

	if strings.Contains(ref, ".") {
		return c.GetOperation(ctx, ref)
	}

	return c.resolveBareOpID(ctx, ref)
}

func (c *Catalog) resolveMethodPath(ctx context.Context, method, path, ref string) (*domain.Operation, error) {
	method = strings.ToUpper(method)

	ids, err := c.exactAliasIDs(ctx, method, path)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		ids, err = c.templatedAliasIDs(ctx, method, path)
		if err != nil {
			return nil, err
		}
	}
	return c.resolveCandidateIDs(ctx, ids, ref)
}

func (c *Catalog) resolveBarePath(ctx context.Context, path, ref string) (*domain.Operation, error) {
	ids, err := c.exactAliasIDs(ctx, "", path)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		ids, err = c.templatedAliasIDs(ctx, "", path)
		if err != nil {
			return nil, err
		}
	}
	return c.resolveCandidateIDs(ctx, ids, ref)
}

func (c *Catalog) resolveBareOpID(ctx context.Context, ref string) (*domain.Operation, error) {
	rows, err := c.db.SQL().QueryContext(ctx, `SELECT id FROM operations WHERE raw_op_id_lc = ?`, strings.ToLower(ref))
	if err != nil {
		return nil, fmt.Errorf("catalog: query bare operationId %q: %w", ref, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("catalog: scan operation id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate bare operationId matches: %w", err)
	}
	return c.resolveCandidateIDs(ctx, ids, ref)
}

// exactAliasIDs returns the distinct operation IDs whose alias exactly
// matches path (and method, if method != "").
func (c *Catalog) exactAliasIDs(ctx context.Context, method, path string) ([]string, error) {
	query := `SELECT DISTINCT operation_id FROM operation_aliases WHERE path = ?`
	args := []any{path}
	if method != "" {
		query += ` AND method = ?`
		args = append(args, method)
	}
	return c.queryIDs(ctx, query, args...)
}

// templatedAliasIDs returns the distinct operation IDs whose alias template
// (method, if method != "") matches path via textutil.PathMatches.
func (c *Catalog) templatedAliasIDs(ctx context.Context, method, path string) ([]string, error) {
	query := `SELECT path, operation_id FROM operation_aliases`
	var args []any
	if method != "" {
		query += ` WHERE method = ?`
		args = append(args, method)
	}

	rows, err := c.db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog: query aliases: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var template, opID string
		if err := rows.Scan(&template, &opID); err != nil {
			return nil, fmt.Errorf("catalog: scan alias: %w", err)
		}
		if textutil.PathMatches(template, path) {
			ids = append(ids, opID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate aliases: %w", err)
	}
	return ids, nil
}

func (c *Catalog) queryIDs(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := c.db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog: query: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("catalog: scan id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate ids: %w", err)
	}
	return ids, nil
}

// resolveCandidateIDs turns a set of matched operation IDs into a single
// operation, or a not-found/conflict error.
func (c *Catalog) resolveCandidateIDs(ctx context.Context, ids []string, ref string) (*domain.Operation, error) {
	ids = dedupSorted(ids)
	switch len(ids) {
	case 0:
		suggestions, _ := c.SuggestOperationIDs(ctx, ref, 5)
		return nil, errs.New(errs.OperationNotFound, "no operation matches %q", ref).WithDetail("suggestions", suggestions)
	case 1:
		return c.GetOperation(ctx, ids[0])
	default:
		return nil, errs.New(errs.Conflict, "operation reference %q is ambiguous", ref).WithDetail("candidates", ids)
	}
}
