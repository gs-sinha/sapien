package docs_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/ingest/docs"
)

func opts(path string) docs.Options {
	return docs.Options{ServiceID: "allocation-service", Path: path}
}

func TestParse_NestedAndDuplicateHeadings(t *testing.T) {
	md := "# Title\n\nIntro text.\n\n## A\n\nbody a\n\n### A\n\nnested body\n\n## A\n\nbody a2\n"
	doc := docs.Parse(md, opts("docs/x.md"))

	require.Len(t, doc.Sections, 4)

	want := []domain.DocSection{
		{ID: "allocation-service/docs/x.md#title", Ord: 0, Heading: "Title", Level: 1, Body: "Intro text."},
		{ID: "allocation-service/docs/x.md#a", Ord: 1, Heading: "A", Level: 2, Body: "body a"},
		{ID: "allocation-service/docs/x.md#a-2", Ord: 2, Heading: "A", Level: 3, Body: "nested body"},
		{ID: "allocation-service/docs/x.md#a-3", Ord: 3, Heading: "A", Level: 2, Body: "body a2"},
	}
	for i, w := range want {
		assert.Equal(t, w.ID, doc.Sections[i].ID, "section %d ID", i)
		assert.Equal(t, w.Ord, doc.Sections[i].Ord, "section %d Ord", i)
		assert.Equal(t, w.Heading, doc.Sections[i].Heading, "section %d Heading", i)
		assert.Equal(t, w.Level, doc.Sections[i].Level, "section %d Level", i)
		assert.Equal(t, w.Body, doc.Sections[i].Body, "section %d Body", i)
	}
}

func TestParse_HeadingInsideFenceIsNotASection(t *testing.T) {
	md := "# Doc\n\n## Section\n\n```\n# not a heading\nstill code\n```\n\nafter fence text\n"
	doc := docs.Parse(md, opts("docs/x.md"))

	require.Len(t, doc.Sections, 2)
	assert.Equal(t, "Doc", doc.Sections[0].Heading)
	assert.Equal(t, "", doc.Sections[0].Body)
	assert.Equal(t, "Section", doc.Sections[1].Heading)
	assert.Equal(t, "```\n# not a heading\nstill code\n```\n\nafter fence text", doc.Sections[1].Body)
}

func TestParse_TildeFenceAlsoHidesHeadings(t *testing.T) {
	md := "## Section\n\n~~~\n# not a heading\n~~~\n"
	doc := docs.Parse(md, opts("docs/x.md"))
	require.Len(t, doc.Sections, 1)
	assert.Equal(t, "Section", doc.Sections[0].Heading)
	assert.Equal(t, "~~~\n# not a heading\n~~~", doc.Sections[0].Body)
}

func TestParse_TextBeforeFirstHeading(t *testing.T) {
	md := "Some preamble.\n\n## First\n\nbody\n"
	o := opts("docs/x.md")
	o.Title = "My Doc"
	doc := docs.Parse(md, o)

	require.Len(t, doc.Sections, 2)
	assert.Equal(t, "My Doc", doc.Sections[0].Heading)
	assert.Equal(t, 1, doc.Sections[0].Level)
	assert.Equal(t, "Some preamble.", doc.Sections[0].Body)
	assert.Equal(t, "First", doc.Sections[1].Heading)
	assert.Equal(t, "body", doc.Sections[1].Body)
}

func TestParse_BlankTextBeforeFirstHeadingProducesNoSection(t *testing.T) {
	md := "   \n\n## First\n\nbody\n"
	doc := docs.Parse(md, opts("docs/x.md"))
	require.Len(t, doc.Sections, 1)
	assert.Equal(t, "First", doc.Sections[0].Heading)
}

func TestParse_EmptyDoc(t *testing.T) {
	doc := docs.Parse("", opts("docs/x.md"))
	assert.Empty(t, doc.Sections)
}

func TestParse_OnlyATitle(t *testing.T) {
	doc := docs.Parse("# Just A Title", opts("docs/x.md"))
	require.Len(t, doc.Sections, 1)
	assert.Equal(t, "Just A Title", doc.Sections[0].Heading)
	assert.Equal(t, 1, doc.Sections[0].Level)
	assert.Equal(t, "", doc.Sections[0].Body)
}

func TestParse_SetextHeadingsAreNotSupported(t *testing.T) {
	// "Foo\n---" looks like a setext H2 in full CommonMark, but this
	// sectioner only recognizes ATX (#) headings, deliberately (PLAN says
	// setext support is optional; we chose not to implement it to keep the
	// hand-rolled sectioner small and unambiguous). It must stay as body
	// text of the enclosing (synthetic, pre-heading) section.
	md := "Foo\n---\n\nbar\n"
	doc := docs.Parse(md, opts("docs/x.md"))
	require.Len(t, doc.Sections, 1)
	assert.Equal(t, doc.Title, doc.Sections[0].Heading)
	assert.Equal(t, 1, doc.Sections[0].Level)
	assert.Equal(t, "Foo\n---\n\nbar", doc.Sections[0].Body)
}

func TestParse_ATXTrailingHashesStripped(t *testing.T) {
	md := "## Heading ##\n\nbody\n"
	doc := docs.Parse(md, opts("docs/x.md"))
	require.Len(t, doc.Sections, 1)
	assert.Equal(t, "Heading", doc.Sections[0].Heading)
}

func TestParse_NoSpaceAfterHashIsNotAHeading(t *testing.T) {
	md := "#NotAHeading\n\nmore text\n"
	doc := docs.Parse(md, opts("docs/x.md"))
	// Entire thing is one blob of pre-heading text (no valid heading found).
	require.Len(t, doc.Sections, 1)
	assert.Equal(t, doc.Title, doc.Sections[0].Heading)
	assert.Equal(t, "#NotAHeading\n\nmore text", doc.Sections[0].Body)
}

func TestParse_MoreThanSixHashesIsNotAHeading(t *testing.T) {
	md := "####### Not A Heading\n\nbody\n"
	doc := docs.Parse(md, opts("docs/x.md"))
	require.Len(t, doc.Sections, 1)
	assert.Equal(t, doc.Title, doc.Sections[0].Heading)
}

func TestParse_IDUniqueness(t *testing.T) {
	md := "# T\n\n## Dup\n\na\n\n## Dup\n\nb\n\n## Dup\n\nc\n"
	doc := docs.Parse(md, opts("docs/x.md"))
	seen := map[string]bool{}
	for _, s := range doc.Sections {
		require.False(t, seen[s.ID], "duplicate ID %s", s.ID)
		seen[s.ID] = true
		assert.Contains(t, s.ID, doc.ID+"#")
	}
}

func TestParse_TitleDefaults(t *testing.T) {
	t.Run("explicit title wins", func(t *testing.T) {
		o := opts("docs/allocation.md")
		o.Title = "Explicit"
		doc := docs.Parse("# H1 Title\n", o)
		assert.Equal(t, "Explicit", doc.Title)
	})
	t.Run("first H1 used", func(t *testing.T) {
		doc := docs.Parse("## Not H1\n\n# Actual H1\n\nbody\n", opts("docs/allocation.md"))
		assert.Equal(t, "Actual H1", doc.Title)
	})
	t.Run("falls back to file name without extension", func(t *testing.T) {
		doc := docs.Parse("## Only H2\n\nbody\n", opts("docs/allocation.md"))
		assert.Equal(t, "allocation", doc.Title)
	})
}

func TestParse_DocIDAndHash(t *testing.T) {
	md := "# Hello\n\nworld\n"
	doc := docs.Parse(md, opts("docs/allocation.md"))
	assert.Equal(t, "allocation-service/docs/allocation.md", doc.ID)
	assert.Equal(t, "allocation-service", doc.ServiceID)
	assert.Equal(t, "docs/allocation.md", doc.Path)
	assert.Equal(t, domain.DocSourceFile, doc.Source)
	assert.Len(t, doc.Hash, 64) // sha256 hex
}

func TestParse_HashStability(t *testing.T) {
	md := "# Hello\n\nworld\n"
	a := docs.Parse(md, opts("docs/allocation.md"))
	b := docs.Parse(md, opts("docs/other.md")) // different opts, same markdown
	assert.Equal(t, a.Hash, b.Hash)

	c := docs.Parse(md+" ", opts("docs/allocation.md"))
	assert.NotEqual(t, a.Hash, c.Hash)
}

func TestParse_Deterministic(t *testing.T) {
	md := "# Hello\n\nSee `allocation-service.allocate` and GET /v1/riders/{riderId}.\n"
	o := opts("docs/allocation.md")
	o.Known = docs.KnownRefs{
		Operations: []string{"allocation-service.allocate"},
		Paths:      []string{"/v1/riders/{riderId}"},
	}
	a := docs.Parse(md, o)
	b := docs.Parse(md, o)
	assert.Equal(t, a, b)
}

func TestParseFile_MissingFileErrors(t *testing.T) {
	_, err := docs.ParseFile("testdata/does-not-exist.md", opts("docs/does-not-exist.md"))
	require.Error(t, err)
}

func TestParseFile_ReadsAndParses(t *testing.T) {
	doc, err := docs.ParseFile("testdata/rider.md", opts("docs/rider.md"))
	require.NoError(t, err)
	assert.Equal(t, "Rider Fields", doc.Title)
	assert.NotEmpty(t, doc.Sections)
}
