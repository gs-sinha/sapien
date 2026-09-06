package search

import (
	"context"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/textutil"
)

// Scores for the ways a pasted path can relate to an operation's path
// template. A URL someone copied from a log or a browser usually carries a
// base path the contract does not (suffix), concrete ids where the template
// has {params} (templated), or stops short at a parent resource (parent).
const (
	urlScoreExact     = 1.0
	urlScoreTemplated = 0.95
	urlScoreSuffix    = 0.85
	urlScoreParent    = 0.7  // minus urlParentStep per missing trailing segment
	urlParentStep     = 0.03 //
	urlScorePrefix    = 0.6  // the template is a prefix of the pasted path
	urlMethodMismatch = 0.6  // multiplier when a method was given and differs
	urlFallbackMin    = 3    // fewer URL hits than this: append lexical hits
	urlLexicalScale   = 0.5  // lexical hits appended after URL hits are scaled by this
	urlParamPenalty   = 0.01 // per {param} segment consumed: a literal match outranks a wildcard one
)

type opAlias struct {
	opID   string
	method string
	path   string
}

// loadAliases returns every (method, path template) alias, optionally
// restricted to one service. A workspace has hundreds to a few thousand of
// them; matching them in Go is cheaper than expressing suffix and parent
// matches in SQL and keeps one code path for every shape.
func (s *Searcher) loadAliases(ctx context.Context, service string) ([]opAlias, error) {
	q := `SELECT a.operation_id, a.method, a.path FROM operation_aliases a
	      JOIN operations o ON o.id = a.operation_id
	      JOIN services sv ON sv.id = o.service_id`
	var args []any
	if service != "" {
		q += ` WHERE sv.name = ?`
		args = append(args, service)
	}
	rows, err := s.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrapf(err, "alias query")
	}
	defer rows.Close()
	var out []opAlias
	for rows.Next() {
		var a opAlias
		if err := rows.Scan(&a.opID, &a.method, &a.path); err != nil {
			return nil, wrapf(err, "alias scan")
		}
		out = append(out, a)
	}
	return out, wrapf(rows.Err(), "alias rows")
}

// urlMatchScore scores how well the pasted path matches template.
func urlMatchScore(template, path string) float64 {
	ts := textutil.PathSegments(template)
	ps := textutil.PathSegments(path)
	if len(ts) == 0 || len(ps) == 0 {
		if len(ts) == 0 && len(ps) == 0 {
			return urlScoreExact
		}
		return 0
	}
	// all reports whether every template segment matches its path segment,
	// and how many of those matches consumed a {param} wildcard, so a
	// literal template outranks a wildcard one for the same path.
	all := func(tseg, pseg []string) (bool, int) {
		params := 0
		for i := range tseg {
			switch {
			case textutil.IsPathParam(tseg[i]):
				params++
			case strings.EqualFold(tseg[i], pseg[i]):
			default:
				return false, 0
			}
		}
		return true, params
	}
	penalty := func(n int) float64 { return urlParamPenalty * float64(n) }
	switch {
	case len(ts) == len(ps):
		if strings.EqualFold(strings.Trim(template, "/"), strings.Trim(path, "/")) {
			return urlScoreExact
		}
		if ok, n := all(ts, ps); ok {
			return urlScoreTemplated - penalty(n)
		}
	case len(ps) > len(ts):
		if ok, n := all(ts, ps[len(ps)-len(ts):]); ok {
			return urlScoreSuffix - penalty(n)
		}
		if ok, n := all(ts, ps[:len(ts)]); ok {
			return urlScorePrefix - penalty(n)
		}
	case len(ps) < len(ts):
		if ok, n := all(ts[:len(ps)], ps); ok {
			return urlScoreParent - urlParentStep*float64(len(ts)-len(ps)) - penalty(n)
		}
	}
	return 0
}

// urlLookup ranks operations by how their path templates relate to a pasted
// URL or path (Operations() calls it whenever the query is path-shaped).
// When fewer than urlFallbackMin operations match, lexical hits for the
// path's tokens are appended at reduced scores so a partial or slightly
// wrong path still gets an answer; when nothing matches at all, the result
// is the plain lexical lookup.
func (s *Searcher) urlLookup(ctx context.Context, method, path string, opts domain.SearchOptions) ([]domain.SearchResult, error) {
	effMethod := method
	if effMethod == "" {
		effMethod = strings.ToUpper(opts.Method)
	}
	aliases, err := s.loadAliases(ctx, opts.Service)
	if err != nil {
		return nil, err
	}

	best := map[string]float64{}
	methodHit := map[string]bool{}
	for _, a := range aliases {
		sc := urlMatchScore(a.path, path)
		if sc == 0 {
			continue
		}
		if effMethod != "" {
			if strings.EqualFold(a.method, effMethod) {
				methodHit[a.opID] = true
			} else {
				sc *= urlMethodMismatch
			}
		}
		if sc > best[a.opID] {
			best[a.opID] = sc
		}
	}

	tokens := textutil.PathTokens(path)
	fallback := meaningfulPathTokens(tokens)
	if len(best) == 0 {
		if len(fallback) == 0 {
			return nil, nil
		}
		return s.lexicalLookup(ctx, strings.Join(fallback, " "), opts)
	}

	ids := make([]string, 0, len(best))
	for id := range best {
		ids = append(ids, id)
	}
	metas, err := s.loadOpMeta(ctx, ids)
	if err != nil {
		return nil, err
	}
	limit := effectiveLimit(opts.Limit)
	list := make([]scoredID, 0, len(ids))
	for _, id := range ids {
		meta, ok := metas[id]
		if !ok {
			continue
		}
		sc := best[id]
		if meta.deprecated && !opts.IncludeDeprecated {
			sc *= 0.5
		}
		list = append(list, scoredID{id: id, score: sc})
	}
	list = sortScored(list, limit)

	results, err := s.buildResults(ctx, list, tokens)
	if err != nil {
		return nil, err
	}
	for i := range results {
		tags := []string{"url"}
		if methodHit[results[i].Operation.ID] {
			tags = append(tags, "method")
		}
		results[i].MatchedOn = append(tags, results[i].MatchedOn...)
	}

	if len(results) < urlFallbackMin && len(results) < limit && len(fallback) > 0 {
		seen := map[string]bool{}
		for _, r := range results {
			seen[r.Operation.ID] = true
		}
		lex, err := s.lexicalLookup(ctx, strings.Join(fallback, " "), opts)
		if err == nil {
			for _, r := range lex {
				if seen[r.Operation.ID] || len(results) >= limit {
					continue
				}
				r.Score *= urlLexicalScale
				results = append(results, r)
			}
		}
	}
	return results, nil
}

// meaningfulPathTokens drops the tokens every path shares (version markers
// such as v1, and "api") so a lexical fallback for a pasted path does not
// match every operation in the catalog on "v1" alone.
func meaningfulPathTokens(tokens []string) []string {
	var out []string
	for _, t := range tokens {
		lt := strings.ToLower(t)
		if lt == "api" || lt == "apis" {
			continue
		}
		if len(lt) >= 2 && lt[0] == 'v' && strings.Trim(lt[1:], "0123456789") == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}
