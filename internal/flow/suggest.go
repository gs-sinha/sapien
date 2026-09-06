package flow

import (
	"sort"
	"strings"

	"github.com/growsimplee/sapien/internal/textutil"
)

// NearestSuggestions is nearestSuggestions, exported for callers outside
// this package that build their own resolver adapters against a fixed
// candidate list -- e.g. internal/engine/local's example resolver ranking
// SuggestExamples -- so "did you mean" ranking stays consistent with the
// UNKNOWN_OPERATION/UNKNOWN_EXAMPLE suggestions computed in here.
func NearestSuggestions(target string, candidates []string, n int) []string {
	return nearestSuggestions(target, candidates, n)
}

// nearestSuggestions ranks candidates by relevance to target: first by the
// number of textutil.SplitIdent tokens they share with target (so
// "qcomSkil" ranks "qcomSkill" above "qcomOnly"), then by whether one is a
// prefix of the other, then alphabetically for a stable order. Only
// candidates with some relation (shared token or prefix) are considered; if
// none qualify, the first n candidates in alphabetical order are returned
// instead so a suggestion is still offered. Returns at most n names.
func nearestSuggestions(target string, candidates []string, n int) []string {
	if len(candidates) == 0 || n <= 0 {
		return nil
	}
	targetTokens := tokenSet(target)
	lowerTarget := strings.ToLower(target)

	type scored struct {
		name   string
		score  int
		prefix bool
	}
	var ranked []scored
	for _, c := range candidates {
		overlap := 0
		for t := range tokenSet(c) {
			if targetTokens[t] {
				overlap++
			}
		}
		lowerC := strings.ToLower(c)
		prefix := strings.HasPrefix(lowerC, lowerTarget) || strings.HasPrefix(lowerTarget, lowerC)
		if overlap > 0 || prefix {
			ranked = append(ranked, scored{name: c, score: overlap, prefix: prefix})
		}
	}

	if len(ranked) == 0 {
		sorted := append([]string(nil), candidates...)
		sort.Strings(sorted)
		if len(sorted) > n {
			sorted = sorted[:n]
		}
		return sorted
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ranked[i].prefix != ranked[j].prefix {
			return ranked[i].prefix
		}
		return ranked[i].name < ranked[j].name
	})

	out := make([]string, 0, n)
	for _, r := range ranked {
		out = append(out, r.name)
		if len(out) == n {
			break
		}
	}
	return out
}

func tokenSet(s string) map[string]bool {
	parts := textutil.SplitIdent(s)
	out := make(map[string]bool, len(parts))
	for _, p := range parts {
		out[p] = true
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
