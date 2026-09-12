package catalog

import (
	"context"
	"fmt"
)

// Stats summarizes the size of the catalog.
func (c *Catalog) Stats(ctx context.Context) (Stats, error) {
	var s Stats
	counts := []struct {
		dst *int
		q   string
	}{
		{&s.Services, `SELECT COUNT(*) FROM services`},
		{&s.Operations, `SELECT COUNT(*) FROM operations`},
		{&s.Tasks, `SELECT COUNT(*) FROM tasks`},
		{&s.Fields, `SELECT COUNT(*) FROM fields`},
		{&s.Docs, `SELECT COUNT(*) FROM docs`},
		{&s.Sections, `SELECT COUNT(*) FROM doc_sections`},
		{&s.Flows, `SELECT COUNT(*) FROM flows`},
	}
	for _, cnt := range counts {
		if err := c.db.SQL().QueryRowContext(ctx, cnt.q).Scan(cnt.dst); err != nil {
			return Stats{}, fmt.Errorf("catalog: stats: %w", err)
		}
	}
	return s, nil
}
