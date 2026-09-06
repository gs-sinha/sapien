package local

import (
	"context"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

// searchAPI implements engine.SearchAPI as a direct pass-through to
// internal/search.
type searchAPI struct{ l *Local }

var _ engine.SearchAPI = (*searchAPI)(nil)

func (s *searchAPI) Operations(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.SearchResult, error) {
	results, err := s.l.srch.Operations(ctx, query, opts)
	if err == nil {
		// Usage feedback (search ranking tuning task, part 2):
		// search_feedback.go's noteOperationUse later looks back at this
		// recorded query/result-set pair when opID is actually used.
		recordSearch(s.l, query, results)
	}
	return results, err
}

func (s *searchAPI) Docs(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.DocSearchResult, error) {
	return s.l.srch.Docs(ctx, query, opts)
}
