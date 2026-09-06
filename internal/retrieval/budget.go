package retrieval

import (
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
)

// maxDescriptionRunes is the length an operation description is truncated
// to by the "descriptions" budget tier.
const maxDescriptionRunes = 200

// maxDocBodyRunes caps how long a doc body is left after the "doc_bodies"
// tier trims it to its first paragraph. A doc section that's already a
// single paragraph (no blank line) would otherwise pass firstParagraph
// unchanged, defeating the tier entirely for exactly the sections most
// likely to be long; capping the (already paragraph-trimmed) result gives
// every doc body tier real leverage under a tight budget.
const maxDocBodyRunes = 80

// enforceBudget implements PLAN.md §14 steps 6-7: while bundle's estimated
// token cost exceeds budget, apply cuts in tier order (runs, examples, doc
// bodies, descriptions, response schema depth, operations - never below 1
// operation), recording how many items each tier cut into bundle.Omitted.
func enforceBudget(bundle *domain.ContextBundle, budget int) {
	within := func() bool { return EstimateTokens(bundle) <= budget }
	omitted := map[string]int{}

	if !within() && len(bundle.Runs) > 0 {
		omitted["runs"] = len(bundle.Runs)
		bundle.Runs = nil
	}

	if !within() {
		if n := dropExamples(bundle); n > 0 {
			omitted["examples"] = n
		}
	}

	if !within() {
		if n := trimDocBodies(bundle); n > 0 {
			omitted["doc_bodies"] = n
		}
	}

	if !within() {
		if n := truncateDescriptions(bundle); n > 0 {
			omitted["descriptions"] = n
		}
	}

	if !within() {
		if n := dropDeepResponseFields(bundle); n > 0 {
			omitted["schema_depth"] = n
		}
	}

	if !within() {
		n := 0
		for len(bundle.Operations) > 1 && !within() {
			bundle.Operations = bundle.Operations[:len(bundle.Operations)-1]
			n++
		}
		if n > 0 {
			omitted["operations"] = n
		}
	}

	if len(omitted) > 0 {
		bundle.Omitted = omitted
	}
}

// dropExamples clears every operation's (contract) Examples and the
// bundle-level examples tier (saved examples, PLAN §34b), returning how
// many items were dropped across both.
func dropExamples(bundle *domain.ContextBundle) int {
	n := 0
	for i := range bundle.Operations {
		if len(bundle.Operations[i].Examples) > 0 {
			bundle.Operations[i].Examples = nil
			n++
		}
	}
	if len(bundle.Examples) > 0 {
		n += len(bundle.Examples)
		bundle.Examples = nil
	}
	return n
}

// trimDocBodies trims every doc's Body to its first paragraph and then, in
// case that paragraph is itself still long (or the body had no blank line
// to begin with), to maxDocBodyRunes (marking Truncated either way).
// Returns how many docs were actually shortened.
func trimDocBodies(bundle *domain.ContextBundle) int {
	n := 0
	for i := range bundle.Docs {
		trimmed := truncateRunes(firstParagraph(bundle.Docs[i].Body), maxDocBodyRunes)
		if trimmed != bundle.Docs[i].Body {
			bundle.Docs[i].Body = trimmed
			bundle.Docs[i].Truncated = true
			n++
		}
	}
	return n
}

// firstParagraph returns text up to (not including) the first blank line,
// trimmed of surrounding whitespace. Text with no blank line is returned
// unchanged.
func firstParagraph(text string) string {
	if idx := strings.Index(text, "\n\n"); idx >= 0 {
		return strings.TrimSpace(text[:idx])
	}
	return text
}

// truncateDescriptions shortens every operation description longer than
// maxDescriptionRunes, returning how many were shortened.
func truncateDescriptions(bundle *domain.ContextBundle) int {
	n := 0
	for i := range bundle.Operations {
		d := bundle.Operations[i].Description
		if len([]rune(d)) > maxDescriptionRunes {
			bundle.Operations[i].Description = truncateRunes(d, maxDescriptionRunes)
			n++
		}
	}
	return n
}

// truncateRunes returns the first n runes of s.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// dropDeepResponseFields removes every rendered Response entry deeper than
// 1 level (PLAN.md §14 step 6 tightens the initial depth<=2 rendering to
// depth<=1 under budget pressure), returning how many entries were dropped.
func dropDeepResponseFields(bundle *domain.ContextBundle) int {
	n := 0
	for i := range bundle.Operations {
		resp := bundle.Operations[i].Response
		kept := resp[:0]
		for _, entry := range resp {
			if renderedFieldDepth(entry) <= 1 {
				kept = append(kept, entry)
			} else {
				n++
			}
		}
		bundle.Operations[i].Response = kept
	}
	return n
}

// renderedFieldDepth recovers a rendered "<name>: <type> ..." field line's
// depth from its name portion (the same depth fieldDepth computes from the
// raw field path, since the label is that path's prefix-relative segment).
func renderedFieldDepth(rendered string) int {
	name := rendered
	if i := strings.Index(rendered, ":"); i >= 0 {
		name = rendered[:i]
	}
	return fieldDepth(name)
}
