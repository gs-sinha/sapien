package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/gs-sinha/sapien/internal/textutil"
)

type opCandidate struct {
	id     string
	bare   string // RawOpID, or the ID suffix after "<service>." when synthesized
	method string
	path   string
}

func (c *Catalog) loadCandidates(ctx context.Context) ([]opCandidate, error) {
	rows, err := c.db.SQL().QueryContext(ctx, `SELECT id, service_id, raw_op_id, method, path FROM operations`)
	if err != nil {
		return nil, fmt.Errorf("catalog: load operation candidates: %w", err)
	}
	defer rows.Close()

	var out []opCandidate
	for rows.Next() {
		var id, serviceID, method, path string
		var rawOpID sql.NullString
		if err := rows.Scan(&id, &serviceID, &rawOpID, &method, &path); err != nil {
			return nil, fmt.Errorf("catalog: scan operation candidate: %w", err)
		}
		bare := rawOpID.String
		if bare == "" {
			bare = strings.TrimPrefix(id, serviceID+".")
		}
		out = append(out, opCandidate{id: id, bare: bare, method: method, path: path})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate operation candidates: %w", err)
	}
	return out, nil
}

// SuggestOperationIDs returns up to n operation IDs nearest to ref, for use
// in "not found" error messages. Ranking (deterministic; ties broken
// alphabetically by ID):
//
//  1. same path, different method (only when ref parses as "METHOD /path" or
//     "/path" and an operation shares its path)
//  2. for an id-shaped ref (contains "."): ascending Levenshtein distance
//     between ref's bare operationId part and each candidate's, then
//     descending shared-identifier-token count (textutil.SplitIdent)
//  3. for a path-shaped ref: descending shared path-token count
//     (textutil.PathTokens)
func (c *Catalog) SuggestOperationIDs(ctx context.Context, ref string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	candidates, err := c.loadCandidates(ctx)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	refMethod, refPath, isPathShaped := textutil.ParseMethodPath(ref)

	var refBare string
	if !isPathShaped {
		refBare = ref
		if i := strings.LastIndex(ref, "."); i >= 0 {
			refBare = ref[i+1:]
		}
	}
	refIdentTokens := map[string]bool{}
	for _, t := range textutil.SplitIdent(refBare) {
		refIdentTokens[t] = true
	}
	refPathTokens := map[string]bool{}
	if isPathShaped {
		for _, t := range textutil.PathTokens(refPath) {
			refPathTokens[t] = true
		}
	}

	type scored struct {
		id                 string
		samePathDiffMethod bool
		primary            int // ascending: lower is a closer match
		overlap            int // descending secondary tiebreak
	}

	results := make([]scored, 0, len(candidates))
	for _, cand := range candidates {
		samePathDiffMethod := isPathShaped && refPath != "" && cand.path == refPath &&
			(refMethod == "" || !strings.EqualFold(cand.method, refMethod))

		var primary, overlap int
		if isPathShaped {
			for _, t := range textutil.PathTokens(cand.path) {
				if refPathTokens[t] {
					overlap++
				}
			}
			primary = -overlap
		} else {
			primary = levenshtein(strings.ToLower(refBare), strings.ToLower(cand.bare))
			for _, t := range textutil.SplitIdent(cand.bare) {
				if refIdentTokens[t] {
					overlap++
				}
			}
		}

		results = append(results, scored{
			id:                 cand.id,
			samePathDiffMethod: samePathDiffMethod,
			primary:            primary,
			overlap:            overlap,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.samePathDiffMethod != b.samePathDiffMethod {
			return a.samePathDiffMethod
		}
		if a.primary != b.primary {
			return a.primary < b.primary
		}
		if a.overlap != b.overlap {
			return a.overlap > b.overlap
		}
		return a.id < b.id
	})

	if n > len(results) {
		n = len(results)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = results[i].id
	}
	return out, nil
}
