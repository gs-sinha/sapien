package search

import (
	"strings"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/textutil"
)

// matchedOn determines, post-hoc, which parts of op the query tokens were
// found in, mirroring the columns the catalog indexes operations_fts with:
// op_id, path, summary, description, tags, and per-leaf "field:<name>" for
// params and flattened schema fields (fieldLeaves, from the fields table).
func matchedOn(tokens []string, op domain.Operation, fieldLeaves []string) []string {
	if len(tokens) == 0 {
		return nil
	}

	var out []string
	add := func(label string) {
		for _, existing := range out {
			if existing == label {
				return
			}
		}
		out = append(out, label)
	}

	opIDText := strings.ToLower(textutil.Join(textutil.SplitIdent(op.ID)) + " " + op.ID + " " + op.RawOpID)
	if containsAnyToken(opIDText, tokens) {
		add("op_id")
	}

	if op.HTTP != nil {
		pathText := strings.ToLower(textutil.Join(textutil.PathTokens(op.HTTP.Path)) + " " + op.HTTP.Path)
		if containsAnyToken(pathText, tokens) {
			add("path")
		}
	}

	if containsAnyToken(strings.ToLower(op.Summary), tokens) {
		add("summary")
	}
	if containsAnyToken(strings.ToLower(op.Description), tokens) {
		add("description")
	}

	tagConcepts := append(append([]string{}, op.Tags...), op.Concepts...)
	tagsText := strings.ToLower(textutil.Join(textutil.Tokens(strings.Join(tagConcepts, " "))))
	if tagsText != "" && containsAnyToken(tagsText, tokens) {
		add("tags")
	}

	for _, p := range op.Params {
		if tokenMatchesName(tokens, p.Name) {
			add("field:" + p.Name)
		}
	}
	for _, leaf := range fieldLeaves {
		if tokenMatchesName(tokens, leaf) {
			add("field:" + leaf)
		}
	}

	return out
}

// containsAnyToken reports whether haystack (already lower-cased) contains
// any of tokens as a substring.
func containsAnyToken(haystack string, tokens []string) bool {
	for _, t := range tokens {
		if t != "" && strings.Contains(haystack, t) {
			return true
		}
	}
	return false
}

// tokenMatchesName reports whether any token matches name, either as a
// substring of its lower-cased form or as one of its identifier parts (so a
// query token "qcom" matches a field named "qcomSkill").
func tokenMatchesName(tokens []string, name string) bool {
	if name == "" {
		return false
	}
	lower := strings.ToLower(name)
	if containsAnyToken(lower, tokens) {
		return true
	}
	for _, part := range textutil.Tokens(name) {
		for _, t := range tokens {
			if t == part {
				return true
			}
		}
	}
	return false
}
