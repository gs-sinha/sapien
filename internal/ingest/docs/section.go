package docs

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
)

// splitLines normalizes line endings and splits markdown into lines.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

// fenceState tracks whether we're inside a fenced code block (``` or ~~~)
// opened by a line with at least 3 of the same fence character.
type fenceState struct {
	active bool
	ch     byte
	length int
}

// parseFenceLine reports whether line looks like a fence delimiter (opening
// or closing): up to 3 leading spaces, then 3+ identical '`' or '~'
// characters. rest is whatever follows those characters (the info string for
// an opening fence; must be blank for a valid closing fence).
func parseFenceLine(line string) (ch byte, length int, rest string, ok bool) {
	i := 0
	for i < len(line) && line[i] == ' ' && i < 3 {
		i++
	}
	if i >= len(line) {
		return 0, 0, "", false
	}
	c := line[i]
	if c != '`' && c != '~' {
		return 0, 0, "", false
	}
	j := i
	for j < len(line) && line[j] == c {
		j++
	}
	n := j - i
	if n < 3 {
		return 0, 0, "", false
	}
	return c, n, line[j:], true
}

// isFenceClose reports whether line closes the currently open fence f.
func isFenceClose(line string, f fenceState) bool {
	ch, n, rest, ok := parseFenceLine(line)
	if !ok || ch != f.ch || n < f.length {
		return false
	}
	return strings.TrimSpace(rest) == ""
}

// atxHeadingRe matches an ATX heading line: up to 3 leading spaces, 1-6 '#'
// characters, then either end-of-line or whitespace followed by the heading
// text. This deliberately rejects "#NoSpace" (not a heading per CommonMark).
var atxHeadingRe = regexp.MustCompile(`^ {0,3}(#{1,6})(?:(\s+)(.*))?$`)

// atxClosingRe strips an optional ATX closing sequence ("## Heading ##"),
// which per CommonMark must be preceded by whitespace.
var atxClosingRe = regexp.MustCompile(`\s+#+\s*$`)

// parseATXHeading parses an ATX heading line outside of a fenced block.
func parseATXHeading(line string) (level int, heading string, ok bool) {
	m := atxHeadingRe.FindStringSubmatch(line)
	if m == nil {
		return 0, "", false
	}
	level = len(m[1])
	text := strings.TrimSpace(m[3])
	text = strings.TrimSpace(atxClosingRe.ReplaceAllString(text, ""))
	return level, text, true
}

// headingLine is a single detected ATX heading with its line index.
type headingLine struct {
	level   int
	heading string
	line    int
}

// scanHeadings finds every ATX heading in lines, skipping headings that
// appear inside fenced code blocks. Setext headings (underlines) are not
// supported; see the package-level notes in docs.go.
func scanHeadings(lines []string) []headingLine {
	var out []headingLine
	fence := fenceState{}
	for i, line := range lines {
		if fence.active {
			if isFenceClose(line, fence) {
				fence.active = false
			}
			continue
		}
		if ch, n, _, ok := parseFenceLine(line); ok {
			fence = fenceState{active: true, ch: ch, length: n}
			continue
		}
		if level, heading, ok := parseATXHeading(line); ok {
			out = append(out, headingLine{level: level, heading: heading, line: i})
		}
	}
	return out
}

// defaultTitle resolves the default doc title: the first H1 heading if any,
// else the file name (from p) without its extension.
func defaultTitle(headings []headingLine, p string) string {
	for _, h := range headings {
		if h.level == 1 {
			return h.heading
		}
	}
	base := path.Base(p)
	return strings.TrimSuffix(base, path.Ext(base))
}

// buildSections splits lines into sections at each heading. Text before the
// first heading becomes a section titled `title` at level 1, if non-blank.
// A section's body is the trimmed text up to (not including) the next
// heading of any level. IDs are not assigned here; see assignSectionIDs.
func buildSections(lines []string, headings []headingLine, title string) []domain.DocSection {
	var sections []domain.DocSection
	ord := 0

	firstHeadingLine := len(lines)
	if len(headings) > 0 {
		firstHeadingLine = headings[0].line
	}
	if pre := strings.TrimSpace(strings.Join(lines[:firstHeadingLine], "\n")); pre != "" {
		sections = append(sections, domain.DocSection{Heading: title, Level: 1, Body: pre, Ord: ord})
		ord++
	}

	for k, h := range headings {
		start := h.line + 1
		end := len(lines)
		if k+1 < len(headings) {
			end = headings[k+1].line
		}
		body := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		sections = append(sections, domain.DocSection{Heading: h.heading, Level: h.level, Body: body, Ord: ord})
		ord++
	}
	return sections
}

// assignSectionIDs sets each section's ID to docID + "#" + slug, appending
// "-2", "-3", ... to duplicate slugs (in document order).
func assignSectionIDs(sections []domain.DocSection, docID string) {
	counts := map[string]int{}
	for i := range sections {
		base := Slug(sections[i].Heading)
		counts[base]++
		slug := base
		if n := counts[base]; n > 1 {
			slug = base + "-" + strconv.Itoa(n)
		}
		sections[i].ID = docID + "#" + slug
	}
}
