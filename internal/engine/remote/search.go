package remote

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

// Operations maps to GET /v1/operations?q=&method=&service=&limit=&include_deprecated=.
// A non-empty query is required to reach the search branch server-side --
// see the package doc for what happens with an empty one.
func (s *searchAPI) Operations(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.SearchResult, error) {
	q := url.Values{"q": {query}}
	if opts.Service != "" {
		q.Set("service", opts.Service)
	}
	if opts.Method != "" {
		q.Set("method", opts.Method)
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.IncludeDeprecated {
		q.Set("include_deprecated", "true")
	}
	var out []domain.SearchResult
	if err := s.r().do(ctx, http.MethodGet, "/v1/operations", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Docs maps to GET /v1/docs?q=&service=&limit=. See the caveat on
// Operations above; the same applies here.
func (s *searchAPI) Docs(ctx context.Context, query string, opts domain.SearchOptions) ([]domain.DocSearchResult, error) {
	q := url.Values{"q": {query}}
	if opts.Service != "" {
		q.Set("service", opts.Service)
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	var out []domain.DocSearchResult
	if err := s.r().do(ctx, http.MethodGet, "/v1/docs", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

var _ engine.SearchAPI = (*searchAPI)(nil)
