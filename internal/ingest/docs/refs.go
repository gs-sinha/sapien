package docs

import (
	"regexp"
	"sort"
	"strings"

	"github.com/growsimplee/sapien/internal/domain"
)

// methodPathRe matches an optional HTTP method followed by whitespace and a
// path-like token (a leading '/' plus segment characters), or a bare
// path-like token on its own. Group 1 is the method (empty if absent);
// group 2 is the raw path.
var methodPathRe = regexp.MustCompile(`(?:\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b[ \t]+)?(/[A-Za-z0-9_\-./{}]+)`)

// backtickRe matches an inline code span: `token`, not spanning newlines.
var backtickRe = regexp.MustCompile("`([^`\n]+)`")

// fieldIdentRe is the shape a backticked token must have to be considered a
// candidate field name: a plain identifier (letters, digits, underscore),
// starting with a letter or underscore. This excludes paths, operation IDs
// (which contain '.' and '-'), and punctuation-laden snippets.
var fieldIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// fieldStopwords are backticked identifiers that match fieldIdentRe but are
// almost never a field name in this context (HTTP verbs, booleans, null).
var fieldStopwords = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true, "delete": true,
	"head": true, "options": true, "true": true, "false": true, "null": true,
	"nil": true, "none": true,
}

// ExtractRefs finds structural references to known operations, paths,
// schemas, concepts, services, and backticked fields within text.
//
// Everything except "METHOD /path" mentions is extracted only from text
// outside fenced code blocks (``` or ~~~); fenced content is otherwise
// ignored, since backticks there are literal characters (not inline code)
// and prose-only matches (bare schema/service/concept names) are usually
// noise inside code samples. "METHOD /path" mentions are recognized both
// inside and outside fences, since curl-style examples are valuable.
//
// The result is deterministic and deduplicated by (kind, value), ordered by
// each ref's first appearance in text.
func ExtractRefs(text string, known KnownRefs) []domain.DocRef {
	lines := splitLines(text)
	offsets := make([]int, len(lines))
	off := 0
	for i, l := range lines {
		offsets[i] = off
		off += len(l) + 1
	}

	type found struct {
		pos   int
		kind  domain.RefKind
		value string
	}
	var matches []found

	// Single forward pass: track fence state, mask fenced lines out for the
	// "outside fence only" matchers below, and extract METHOD/path mentions
	// from every line (raw, unmasked), honoring the fence exception.
	maskedLines := make([]string, len(lines))
	fence := fenceState{}
	for i, line := range lines {
		insideFence := fence.active
		switch {
		case fence.active:
			if isFenceClose(line, fence) {
				fence.active = false
			}
			maskedLines[i] = strings.Repeat(" ", len(line))
		default:
			if ch, n, _, ok := parseFenceLine(line); ok {
				fence = fenceState{active: true, ch: ch, length: n}
				maskedLines[i] = strings.Repeat(" ", len(line))
			} else {
				maskedLines[i] = line
			}
		}

		for _, sm := range methodPathRe.FindAllStringSubmatchIndex(line, -1) {
			method := ""
			if sm[2] != -1 {
				method = line[sm[2]:sm[3]]
			}
			raw := line[sm[4]:sm[5]]
			if insideFence && method == "" {
				continue // bare path mentions inside fences are not extracted
			}
			kind, value, ok := classifyPathMention(method, raw, known)
			if !ok {
				continue
			}
			matches = append(matches, found{pos: offsets[i] + sm[4], kind: kind, value: value})
		}
	}
	masked := strings.Join(maskedLines, "\n")

	addAll := func(re *regexp.Regexp, kind domain.RefKind, value string) {
		for _, loc := range re.FindAllStringIndex(masked, -1) {
			matches = append(matches, found{pos: loc[0], kind: kind, value: value})
		}
	}
	for _, id := range known.Operations {
		addAll(wordRe(id), domain.RefOperation, id)
	}
	for _, name := range known.Schemas {
		addAll(wordRe(name), domain.RefSchema, name)
	}
	for _, name := range known.Services {
		addAll(wordReCI(name), domain.RefService, name)
	}
	for _, phrase := range known.Concepts {
		if re := phraseReCI(phrase); re != nil {
			addAll(re, domain.RefConcept, phrase)
		}
	}

	for _, sm := range backtickRe.FindAllStringSubmatchIndex(masked, -1) {
		token := strings.TrimSpace(masked[sm[2]:sm[3]])
		if token == "" || !fieldIdentRe.MatchString(token) {
			continue
		}
		if fieldStopwords[strings.ToLower(token)] {
			continue
		}
		if containsString(known.Schemas, token) {
			continue // already a RefSchema match above; avoid double-classifying
		}
		matches = append(matches, found{pos: sm[2], kind: domain.RefField, value: token})
	}

	sort.SliceStable(matches, func(a, b int) bool { return matches[a].pos < matches[b].pos })

	seen := make(map[domain.RefKind]map[string]bool, 6)
	var refs []domain.DocRef
	for _, m := range matches {
		byKind, ok := seen[m.kind]
		if !ok {
			byKind = map[string]bool{}
			seen[m.kind] = byKind
		}
		if byKind[m.value] {
			continue
		}
		byKind[m.value] = true
		refs = append(refs, domain.DocRef{Kind: m.kind, Value: m.value})
	}
	return refs
}

// classifyPathMention decides what a "[METHOD] /path" mention resolves to:
// RefOperation (via known.Aliases) when a method is present and resolvable,
// else RefPath with the matched known path template. ok is false when raw
// doesn't match any entry in known.Paths.
func classifyPathMention(method, raw string, known KnownRefs) (kind domain.RefKind, value string, ok bool) {
	raw = strings.TrimRight(raw, "./")
	tmpl, ok := matchKnownPath(raw, known.Paths)
	if !ok {
		return "", "", false
	}
	if method != "" {
		if opID, ok := resolveOperation(method, tmpl, known); ok {
			return domain.RefOperation, opID, true
		}
	}
	return domain.RefPath, tmpl, true
}

// resolveOperation looks up the operation ID for method+tmpl via
// known.Aliases, gated by known.Methods when that path has a declared method
// list.
func resolveOperation(method, tmpl string, known KnownRefs) (string, bool) {
	if allowed, has := known.Methods[tmpl]; has {
		ok := false
		for _, m := range allowed {
			if strings.EqualFold(m, method) {
				ok = true
				break
			}
		}
		if !ok {
			return "", false
		}
	}
	id, ok := known.Aliases[strings.ToUpper(method)+" "+tmpl]
	return id, ok
}

// matchKnownPath finds the first template in paths that raw matches,
// segment by segment, treating "{name}" template segments as wildcards for
// any non-empty concrete segment.
func matchKnownPath(raw string, paths []string) (string, bool) {
	for _, tmpl := range paths {
		if pathMatchesTemplate(raw, tmpl) {
			return tmpl, true
		}
	}
	return "", false
}

func pathMatchesTemplate(raw, tmpl string) bool {
	rawSegs := strings.Split(strings.Trim(raw, "/"), "/")
	tmplSegs := strings.Split(strings.Trim(tmpl, "/"), "/")
	if len(rawSegs) != len(tmplSegs) {
		return false
	}
	for i, ts := range tmplSegs {
		if len(ts) >= 2 && ts[0] == '{' && ts[len(ts)-1] == '}' {
			if rawSegs[i] == "" {
				return false
			}
			continue
		}
		if rawSegs[i] != ts {
			return false
		}
	}
	return true
}

func wordRe(s string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(s) + `\b`)
}

func wordReCI(s string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(s) + `\b`)
}

// phraseReCI builds a case-insensitive, whitespace-flexible regexp matching
// phrase as whole words (so a phrase soft-wrapped across a markdown line
// break still matches). Returns nil for a blank phrase.
func phraseReCI(phrase string) *regexp.Regexp {
	words := strings.Fields(phrase)
	if len(words) == 0 {
		return nil
	}
	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = regexp.QuoteMeta(w)
	}
	return regexp.MustCompile(`(?i)\b` + strings.Join(parts, `\s+`) + `\b`)
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
