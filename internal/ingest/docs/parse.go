package docs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/gs-sinha/sapien/internal/domain"
)

// Parse splits markdown into sections and extracts refs per opts.
//
// Doc.ID is opts.ServiceID + "/" + opts.Path. Doc.Hash is the sha256 hex
// digest of markdown as given (no normalization). Title defaults to the
// first H1 heading, else the file name (from opts.Path) without its
// extension.
func Parse(markdown string, opts Options) domain.Doc {
	docID := opts.ServiceID + "/" + opts.Path

	lines := splitLines(markdown)
	headings := scanHeadings(lines)

	title := opts.Title
	if title == "" {
		title = defaultTitle(headings, opts.Path)
	}

	sections := buildSections(lines, headings, title)
	assignSectionIDs(sections, docID)
	// One matcher for the whole document (and, when the caller supplied
	// one, for the whole package): compiling Known's ~700 name regexps once
	// per section is what made doc indexing the daemon's biggest allocator.
	matcher := opts.Matcher
	if matcher == nil {
		matcher = NewRefMatcher(opts.Known)
	}
	for i := range sections {
		sections[i].Refs = matcher.Extract(sections[i].Body)
	}

	source := opts.Source
	if source == "" {
		source = domain.DocSourceFile
	}

	sum := sha256.Sum256([]byte(markdown))

	return domain.Doc{
		ID:        docID,
		ServiceID: opts.ServiceID,
		Path:      opts.Path,
		Title:     title,
		Source:    source,
		Hash:      hex.EncodeToString(sum[:]),
		Sections:  sections,
	}
}

// ParseFile reads path and parses it as markdown. opts.Path (not path) is
// used as the Doc's relative path/ID component; path is only where the
// bytes come from on disk.
func ParseFile(path string, opts Options) (domain.Doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Doc{}, fmt.Errorf("ingest/docs: read %s: %w", path, err)
	}
	return Parse(string(data), opts), nil
}
