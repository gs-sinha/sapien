package enginetest

import (
	"context"
	"sort"
	"strings"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

// Operations does a naive case-insensitive substring match over operation
// ID, summary, and description. Good enough to exercise the wire format; not
// a ranking model.
func (s *searchAPI) Operations(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.SearchResult, error) {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Search.Operations", map[string]any{"query": query, "opts": opts})

	q := strings.ToLower(strings.TrimSpace(query))
	var out []domain.SearchResult
	for _, op := range f.operations {
		if opts.Service != "" && op.ServiceID != opts.Service {
			continue
		}
		if opts.Method != "" && (op.HTTP == nil || !strings.EqualFold(op.HTTP.Method, opts.Method)) {
			continue
		}
		if !opts.IncludeDeprecated && op.Deprecated {
			continue
		}

		var matched []string
		if q == "" {
			matched = []string{"all"}
		} else {
			if strings.Contains(strings.ToLower(op.ID), q) {
				matched = append(matched, "op_id")
			}
			if op.HTTP != nil && strings.Contains(strings.ToLower(op.HTTP.Path), q) {
				matched = append(matched, "path")
			}
			if strings.Contains(strings.ToLower(op.Summary), q) {
				matched = append(matched, "summary")
			}
		}
		if len(matched) == 0 {
			continue
		}
		out = append(out, domain.SearchResult{Operation: op, Score: float64(len(matched)), MatchedOn: matched})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Operation.ID < out[j].Operation.ID
	})
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

// Docs does a naive case-insensitive substring match over doc section
// headings and bodies.
func (s *searchAPI) Docs(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.DocSearchResult, error) {
	f := s.f()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked("Search.Docs", map[string]any{"query": query, "opts": opts})

	q := strings.ToLower(strings.TrimSpace(query))
	var out []domain.DocSearchResult
	for _, d := range f.docs {
		if opts.Service != "" && d.ServiceID != opts.Service {
			continue
		}
		for _, sec := range d.Sections {
			hay := strings.ToLower(sec.Heading + " " + sec.Body)
			if q != "" && !strings.Contains(hay, q) {
				continue
			}
			out = append(out, domain.DocSearchResult{
				Service:   d.ServiceID,
				DocID:     d.ID,
				Path:      d.Path,
				Title:     d.Title,
				SectionID: sec.ID,
				Heading:   sec.Heading,
				Snippet:   sec.Body,
				Refs:      sec.Refs,
				Score:     1,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].SectionID < out[j].SectionID })
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

var _ engine.SearchAPI = (*searchAPI)(nil)
