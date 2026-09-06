package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

// DocSectionRef is a doc section matched by DocsReferencing, together with
// enough context (its doc and service) to locate it; domain.DocSection alone
// doesn't carry either.
type DocSectionRef struct {
	Section domain.DocSection
	DocID   string
	Service string
}

// ListDocs returns every doc for service (or every doc, if service is ""),
// without section bodies (headings are included).
func (c *Catalog) ListDocs(ctx context.Context, service string) ([]domain.Doc, error) {
	query := `SELECT id, service_id, path, title, source, hash FROM docs`
	var args []any
	if service != "" {
		query += ` WHERE service_id = ?`
		args = append(args, service)
	}
	query += ` ORDER BY service_id, path`

	rows, err := c.db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog: list docs: %w", err)
	}
	var docs []domain.Doc
	for rows.Next() {
		var d domain.Doc
		var source string
		if err := rows.Scan(&d.ID, &d.ServiceID, &d.Path, &d.Title, &source, &d.Hash); err != nil {
			rows.Close()
			return nil, fmt.Errorf("catalog: scan doc: %w", err)
		}
		d.Source = domain.DocSource(source)
		docs = append(docs, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("catalog: iterate docs: %w", err)
	}
	rows.Close()

	for i := range docs {
		secs, err := c.sectionHeadingsForDoc(ctx, docs[i].ID)
		if err != nil {
			return nil, err
		}
		docs[i].Sections = secs
	}
	return docs, nil
}

func (c *Catalog) sectionHeadingsForDoc(ctx context.Context, docID string) ([]domain.DocSection, error) {
	rows, err := c.db.SQL().QueryContext(ctx,
		`SELECT id, ord, heading, level FROM doc_sections WHERE doc_id = ? ORDER BY ord`, docID)
	if err != nil {
		return nil, fmt.Errorf("catalog: list doc section headings for %q: %w", docID, err)
	}
	defer rows.Close()

	var out []domain.DocSection
	for rows.Next() {
		var sec domain.DocSection
		if err := rows.Scan(&sec.ID, &sec.Ord, &sec.Heading, &sec.Level); err != nil {
			return nil, fmt.Errorf("catalog: scan doc section heading: %w", err)
		}
		out = append(out, sec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate doc section headings: %w", err)
	}
	return out, nil
}

// GetDoc returns the full doc (sections with bodies, and refs) at path
// within service, or errs.DocNotFound.
func (c *Catalog) GetDoc(ctx context.Context, service, path string) (*domain.Doc, error) {
	var d domain.Doc
	var source string
	err := c.db.SQL().QueryRowContext(ctx,
		`SELECT id, service_id, path, title, source, hash FROM docs WHERE service_id = ? AND path = ?`,
		service, path,
	).Scan(&d.ID, &d.ServiceID, &d.Path, &d.Title, &source, &d.Hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errs.New(errs.DocNotFound, "doc %q not found in service %q", path, service)
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get doc %q/%q: %w", service, path, err)
	}
	d.Source = domain.DocSource(source)

	sections, err := c.sectionsForDoc(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	d.Sections = sections
	return &d, nil
}

func (c *Catalog) sectionsForDoc(ctx context.Context, docID string) ([]domain.DocSection, error) {
	rows, err := c.db.SQL().QueryContext(ctx,
		`SELECT id, ord, heading, level, body FROM doc_sections WHERE doc_id = ? ORDER BY ord`, docID)
	if err != nil {
		return nil, fmt.Errorf("catalog: list doc sections for %q: %w", docID, err)
	}
	var out []domain.DocSection
	for rows.Next() {
		var sec domain.DocSection
		if err := rows.Scan(&sec.ID, &sec.Ord, &sec.Heading, &sec.Level, &sec.Body); err != nil {
			rows.Close()
			return nil, fmt.Errorf("catalog: scan doc section: %w", err)
		}
		out = append(out, sec)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("catalog: iterate doc sections: %w", err)
	}
	rows.Close()

	for i := range out {
		refs, err := c.refsForSection(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Refs = refs
	}
	return out, nil
}

func (c *Catalog) refsForSection(ctx context.Context, sectionID string) ([]domain.DocRef, error) {
	rows, err := c.db.SQL().QueryContext(ctx,
		`SELECT kind, value FROM doc_refs WHERE section_id = ? ORDER BY kind, value`, sectionID)
	if err != nil {
		return nil, fmt.Errorf("catalog: list doc refs for %q: %w", sectionID, err)
	}
	defer rows.Close()

	var out []domain.DocRef
	for rows.Next() {
		var kind, value string
		if err := rows.Scan(&kind, &value); err != nil {
			return nil, fmt.Errorf("catalog: scan doc ref: %w", err)
		}
		out = append(out, domain.DocRef{Kind: domain.RefKind(kind), Value: value})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate doc refs: %w", err)
	}
	return out, nil
}

// GetDocSection returns one section by ID, along with its parent doc's
// metadata (Sections left nil on the returned Doc; the caller already has
// the one section it asked for). Returns (nil, nil, nil) if sectionID
// doesn't exist.
func (c *Catalog) GetDocSection(ctx context.Context, sectionID string) (*domain.DocSection, *domain.Doc, error) {
	var sec domain.DocSection
	var docID string
	err := c.db.SQL().QueryRowContext(ctx,
		`SELECT id, doc_id, ord, heading, level, body FROM doc_sections WHERE id = ?`, sectionID,
	).Scan(&sec.ID, &docID, &sec.Ord, &sec.Heading, &sec.Level, &sec.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("catalog: get doc section %q: %w", sectionID, err)
	}

	refs, err := c.refsForSection(ctx, sec.ID)
	if err != nil {
		return nil, nil, err
	}
	sec.Refs = refs

	var d domain.Doc
	var source string
	err = c.db.SQL().QueryRowContext(ctx,
		`SELECT id, service_id, path, title, source, hash FROM docs WHERE id = ?`, docID,
	).Scan(&d.ID, &d.ServiceID, &d.Path, &d.Title, &source, &d.Hash)
	if err != nil {
		return nil, nil, fmt.Errorf("catalog: get doc %q for section %q: %w", docID, sectionID, err)
	}
	d.Source = domain.DocSource(source)

	return &sec, &d, nil
}

// DocsReferencing returns the doc sections whose refs include (kind, value),
// ordered by service, doc, then section order, capped at limit (no cap if
// limit <= 0). Each result's Section.Refs is left empty: the caller already
// knows the (kind, value) that matched.
func (c *Catalog) DocsReferencing(ctx context.Context, kind domain.RefKind, value string, limit int) ([]DocSectionRef, error) {
	query := `
		SELECT ds.id, ds.doc_id, ds.ord, ds.heading, ds.level, ds.body, d.service_id
		FROM doc_refs r
		JOIN doc_sections ds ON ds.id = r.section_id
		JOIN docs d ON d.id = ds.doc_id
		WHERE r.kind = ? AND r.value = ?
		ORDER BY d.service_id, ds.doc_id, ds.ord
	`
	args := []any{string(kind), value}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := c.db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog: docs referencing %s=%q: %w", kind, value, err)
	}
	defer rows.Close()

	var out []DocSectionRef
	for rows.Next() {
		var sr DocSectionRef
		if err := rows.Scan(&sr.Section.ID, &sr.DocID, &sr.Section.Ord, &sr.Section.Heading, &sr.Section.Level, &sr.Section.Body, &sr.Service); err != nil {
			return nil, fmt.Errorf("catalog: scan doc section ref: %w", err)
		}
		out = append(out, sr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate doc section refs: %w", err)
	}
	return out, nil
}
