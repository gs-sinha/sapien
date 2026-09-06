// Package docs splits Markdown documentation files into heading-delimited
// sections and extracts structural references (operations, paths, schemas,
// fields, concepts, services) from their text, per PLAN.md §5
// ("Documentation model"), §14, and §16.
//
// The package deliberately does not depend on a Markdown library: docs only
// need heading detection, fenced-code-block awareness, and plain text. A
// small hand-rolled sectioner keeps behavior predictable and dependency-free.
package docs

import "github.com/gs-sinha/sapien/internal/domain"

// KnownRefs is the catalog of names ExtractRefs looks for in doc text. All
// slices/maps are optional; a nil or empty KnownRefs simply yields no refs.
type KnownRefs struct {
	// Operations lists full operation IDs, e.g. "allocation-service.allocate".
	Operations []string
	// Paths lists contract paths, possibly templated, e.g.
	// "/v1/allocations", "/v1/riders/{riderId}".
	Paths []string
	// Methods optionally maps a path (as it appears in Paths) to the HTTP
	// methods it supports. When present for a path, it gates operation
	// resolution: a "METHOD /path" mention whose method isn't listed here is
	// never resolved to an operation via Aliases, even if a same-string
	// alias exists; it is instead reported as a RefPath. Paths absent from
	// Methods are unconstrained (resolution relies on Aliases alone).
	Methods map[string][]string
	// Aliases maps "METHOD /path" (path as it appears in Paths, i.e. the
	// template) to the operation ID it resolves to.
	Aliases map[string]string
	// Schemas lists component schema names, e.g. "Rider". Matched as whole
	// words (case-sensitive), including inside backticks.
	Schemas []string
	// Concepts lists free-text concept phrases, e.g. "rider allocation".
	// Matched case-insensitively as whole phrases.
	Concepts []string
	// Services lists service names. Matched as whole tokens,
	// case-insensitively.
	Services []string
}

// Options configures Parse/ParseFile.
type Options struct {
	ServiceID string
	// Path is the path of the doc relative to the service package, e.g.
	// "docs/allocation.md".
	Path string
	// Title overrides the default title (first H1, else the file name
	// without its extension).
	Title string
	// Source defaults to domain.DocSourceFile.
	Source domain.DocSource
	Known  KnownRefs
}
