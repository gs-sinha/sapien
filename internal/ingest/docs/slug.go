package docs

import (
	"regexp"
	"strings"
)

var slugTokenPattern = regexp.MustCompile(`[a-z0-9]+`)

// Slug lower-cases s and joins its runs of [a-z0-9] with '-'. Anything else
// (punctuation, whitespace, non-ASCII) acts as a separator and is dropped, so
// the result never has leading/trailing/duplicate hyphens.
func Slug(s string) string {
	parts := slugTokenPattern.FindAllString(strings.ToLower(s), -1)
	return strings.Join(parts, "-")
}
