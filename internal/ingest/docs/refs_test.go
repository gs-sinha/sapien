package docs_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/ingest/docs"
)

func TestExtractRefs_OperationTokenWholeWord(t *testing.T) {
	text := "Call `allocation-service.allocate` here; do not confuse with " +
		"allocation-service.allocateRider which is unrelated."
	known := docs.KnownRefs{Operations: []string{"allocation-service.allocate"}}
	refs := docs.ExtractRefs(text, known)
	assert.Equal(t, []domain.DocRef{{Kind: domain.RefOperation, Value: "allocation-service.allocate"}}, refs)
}

func TestExtractRefs_TemplatedMentionResolvesViaAlias(t *testing.T) {
	// The exact case called out in the spec: "GET /v1/riders/{riderId}"
	// (the literal template, not a concrete ID) must still resolve.
	text := "GET /v1/riders/{riderId} returns the rider."
	known := docs.KnownRefs{
		Paths:   []string{"/v1/riders/{riderId}"},
		Aliases: map[string]string{"GET /v1/riders/{riderId}": "rider-service.getRider"},
	}
	refs := docs.ExtractRefs(text, known)
	assert.Equal(t, []domain.DocRef{{Kind: domain.RefOperation, Value: "rider-service.getRider"}}, refs)
}

func TestExtractRefs_ConcreteMentionMatchesTemplateAndResolves(t *testing.T) {
	text := "GET /v1/riders/R123 fetches one rider."
	known := docs.KnownRefs{
		Paths:   []string{"/v1/riders/{riderId}"},
		Aliases: map[string]string{"GET /v1/riders/{riderId}": "rider-service.getRider"},
	}
	refs := docs.ExtractRefs(text, known)
	assert.Equal(t, []domain.DocRef{{Kind: domain.RefOperation, Value: "rider-service.getRider"}}, refs)
}

func TestExtractRefs_UnresolvedMethodPathBecomesRefPath(t *testing.T) {
	// Same path/method as above, but no alias registered: falls back to path.
	text := "GET /v1/riders/{riderId} returns the rider."
	known := docs.KnownRefs{Paths: []string{"/v1/riders/{riderId}"}}
	refs := docs.ExtractRefs(text, known)
	assert.Equal(t, []domain.DocRef{{Kind: domain.RefPath, Value: "/v1/riders/{riderId}"}}, refs)
}

func TestExtractRefs_BarePathConcreteMatchesTemplate(t *testing.T) {
	text := "/v1/riders/R123 is a concrete instance."
	known := docs.KnownRefs{Paths: []string{"/v1/riders/{riderId}"}}
	refs := docs.ExtractRefs(text, known)
	assert.Equal(t, []domain.DocRef{{Kind: domain.RefPath, Value: "/v1/riders/{riderId}"}}, refs)
}

func TestExtractRefs_UnknownPathNotExtracted(t *testing.T) {
	text := "/health is not a tracked path."
	known := docs.KnownRefs{Paths: []string{"/v1/riders/{riderId}"}}
	refs := docs.ExtractRefs(text, known)
	assert.Empty(t, refs)
}

func TestExtractRefs_MethodsGateBlocksMismatchedMethod(t *testing.T) {
	text := "POST /v1/allocations is not actually supported that way."
	known := docs.KnownRefs{
		Paths:   []string{"/v1/allocations"},
		Methods: map[string][]string{"/v1/allocations": {"GET"}},
		Aliases: map[string]string{"POST /v1/allocations": "allocation-service.allocate"},
	}
	refs := docs.ExtractRefs(text, known)
	assert.Equal(t, []domain.DocRef{{Kind: domain.RefPath, Value: "/v1/allocations"}}, refs)
}

func TestExtractRefs_MethodsGateAllowsDeclaredMethod(t *testing.T) {
	text := "POST /v1/allocations creates one."
	known := docs.KnownRefs{
		Paths:   []string{"/v1/allocations"},
		Methods: map[string][]string{"/v1/allocations": {"POST", "GET"}},
		Aliases: map[string]string{"POST /v1/allocations": "allocation-service.allocate"},
	}
	refs := docs.ExtractRefs(text, known)
	assert.Equal(t, []domain.DocRef{{Kind: domain.RefOperation, Value: "allocation-service.allocate"}}, refs)
}

func TestExtractRefs_MethodPathAllowedInsideFence(t *testing.T) {
	text := "```\nPOST /v1/allocations\n```"
	known := docs.KnownRefs{
		Paths:   []string{"/v1/allocations"},
		Aliases: map[string]string{"POST /v1/allocations": "allocation-service.allocate"},
	}
	refs := docs.ExtractRefs(text, known)
	assert.Equal(t, []domain.DocRef{{Kind: domain.RefOperation, Value: "allocation-service.allocate"}}, refs)
}

func TestExtractRefs_BarePathInsideFenceExcluded(t *testing.T) {
	text := "```\n/v1/allocations\n```"
	known := docs.KnownRefs{Paths: []string{"/v1/allocations"}}
	refs := docs.ExtractRefs(text, known)
	assert.Empty(t, refs)
}

func TestExtractRefs_SchemaWholeWord(t *testing.T) {
	text := "A `Rider` record. Riders (plural) should not match. RiderProfile should not match either."
	known := docs.KnownRefs{Schemas: []string{"Rider"}}
	refs := docs.ExtractRefs(text, known)
	assert.Equal(t, []domain.DocRef{{Kind: domain.RefSchema, Value: "Rider"}}, refs)
}

func TestExtractRefs_SchemaExtractedFromInlineCode(t *testing.T) {
	text := "See `Rider` for details."
	known := docs.KnownRefs{Schemas: []string{"Rider"}}
	refs := docs.ExtractRefs(text, known)
	assert.Equal(t, []domain.DocRef{{Kind: domain.RefSchema, Value: "Rider"}}, refs)
}

func TestExtractRefs_SchemaNotExtractedInsideFence(t *testing.T) {
	text := "```\nRider\n```"
	known := docs.KnownRefs{Schemas: []string{"Rider"}}
	refs := docs.ExtractRefs(text, known)
	assert.Empty(t, refs)
}

func TestExtractRefs_ConceptCaseInsensitivePhrase(t *testing.T) {
	text := "This covers Rider Allocation flows and general Dispatch strategy, " +
		"though predispatched orders are out of scope."
	known := docs.KnownRefs{Concepts: []string{"rider allocation", "dispatch"}}
	refs := docs.ExtractRefs(text, known)
	assert.Equal(t, []domain.DocRef{
		{Kind: domain.RefConcept, Value: "rider allocation"},
		{Kind: domain.RefConcept, Value: "dispatch"},
	}, refs)
}

func TestExtractRefs_ConceptPhraseSoftWrapAcrossLines(t *testing.T) {
	text := "This section covers rider\nallocation rules."
	known := docs.KnownRefs{Concepts: []string{"rider allocation"}}
	refs := docs.ExtractRefs(text, known)
	assert.Equal(t, []domain.DocRef{{Kind: domain.RefConcept, Value: "rider allocation"}}, refs)
}

func TestExtractRefs_ServiceCaseInsensitiveWholeToken(t *testing.T) {
	text := "The Allocation-Service team owns this; unrelated-service should not match."
	known := docs.KnownRefs{Services: []string{"allocation-service"}}
	refs := docs.ExtractRefs(text, known)
	assert.Equal(t, []domain.DocRef{{Kind: domain.RefService, Value: "allocation-service"}}, refs)
}

func TestExtractRefs_FieldBacktickedIdentifiers(t *testing.T) {
	text := "Field `qcomSkill` and `upcoming_trips` matter. Not fields: " +
		"`/v1/allocations`, `allocation-service.allocate`, `GET`, `Rider`."
	known := docs.KnownRefs{Schemas: []string{"Rider"}}
	refs := docs.ExtractRefs(text, known)
	assert.Equal(t, []domain.DocRef{
		{Kind: domain.RefField, Value: "qcomSkill"},
		{Kind: domain.RefField, Value: "upcoming_trips"},
		{Kind: domain.RefSchema, Value: "Rider"},
	}, refs)
}

func TestExtractRefs_PathTemplateEmptySegmentDoesNotMatch(t *testing.T) {
	text := "/v1//x is malformed and must not match."
	known := docs.KnownRefs{Paths: []string{"/v1/{a}/{b}"}}
	refs := docs.ExtractRefs(text, known)
	assert.Empty(t, refs)
}

func TestExtractRefs_PathSameSegmentCountDifferentLiteralSegment(t *testing.T) {
	text := "/v2/riders/123 must not match the v1 template."
	known := docs.KnownRefs{Paths: []string{"/v1/riders/{riderId}"}}
	refs := docs.ExtractRefs(text, known)
	assert.Empty(t, refs)
}

func TestExtractRefs_BlankConceptIsIgnored(t *testing.T) {
	text := "Nothing special here."
	known := docs.KnownRefs{Concepts: []string{"   "}}
	refs := docs.ExtractRefs(text, known)
	assert.Empty(t, refs)
}

func TestExtractRefs_EmptyKnownRefsYieldsNothing(t *testing.T) {
	refs := docs.ExtractRefs("Nothing here should match anything at all.", docs.KnownRefs{})
	assert.Empty(t, refs)
}

func TestExtractRefs_Deterministic(t *testing.T) {
	text := "See `allocation-service.allocate`, GET /v1/riders/{riderId}, `Rider`, " +
		"`qcomSkill`, rider allocation, and dispatch, plus the allocation-service " +
		"and rider-service teams."
	known := docs.KnownRefs{
		Operations: []string{"allocation-service.allocate"},
		Paths:      []string{"/v1/riders/{riderId}"},
		Aliases:    map[string]string{"GET /v1/riders/{riderId}": "rider-service.getRider"},
		Schemas:    []string{"Rider"},
		Concepts:   []string{"rider allocation", "dispatch"},
		Services:   []string{"allocation-service", "rider-service"},
	}
	a := docs.ExtractRefs(text, known)
	b := docs.ExtractRefs(text, known)
	assert.Equal(t, a, b)
	assert.NotEmpty(t, a)
}

// --- Golden tests over realistic logistics-domain fixtures. ---

func allocationKnownRefs() docs.KnownRefs {
	return docs.KnownRefs{
		Operations: []string{"allocation-service.allocate"},
		Paths: []string{
			"/v1/allocations",
			"/v1/riders/{riderId}/allocation",
			"/v1/riders/{riderId}",
		},
		Methods: map[string][]string{
			"/v1/allocations":                 {"POST"},
			"/v1/riders/{riderId}/allocation": {"GET"},
		},
		Aliases: map[string]string{
			"POST /v1/allocations": "allocation-service.allocate",
		},
		Schemas:  []string{"Rider", "Allocation"},
		Concepts: []string{"rider allocation", "dispatch"},
		Services: []string{"allocation-service", "rider-service"},
	}
}

func TestGolden_Allocation(t *testing.T) {
	o := docs.Options{ServiceID: "allocation-service", Path: "docs/allocation.md", Known: allocationKnownRefs()}
	doc, err := docs.ParseFile("testdata/allocation.md", o)
	require.NoError(t, err)

	require.Len(t, doc.Sections, 4)
	require.Equal(t, "Allocation Rules", doc.Title)

	headings := make([]string, len(doc.Sections))
	for i, s := range doc.Sections {
		headings[i] = s.Heading
	}
	assert.Equal(t, []string{"Allocation Rules", "Overview", "QCOM Skill Matching", "Endpoints"}, headings)

	byHeading := map[string]domain.DocSection{}
	for _, s := range doc.Sections {
		byHeading[s.Heading] = s
	}

	assert.ElementsMatch(t, []domain.DocRef{
		{Kind: domain.RefConcept, Value: "rider allocation"},
		{Kind: domain.RefConcept, Value: "dispatch"},
		{Kind: domain.RefService, Value: "allocation-service"},
	}, byHeading["Allocation Rules"].Refs)

	assert.ElementsMatch(t, []domain.DocRef{
		{Kind: domain.RefService, Value: "allocation-service"},
		{Kind: domain.RefSchema, Value: "Allocation"},
		{Kind: domain.RefSchema, Value: "Rider"},
		{Kind: domain.RefConcept, Value: "rider allocation"},
		{Kind: domain.RefField, Value: "qcomSkill"},
	}, byHeading["Overview"].Refs)

	qcom := byHeading["QCOM Skill Matching"].Refs
	assert.ElementsMatch(t, []domain.DocRef{
		{Kind: domain.RefSchema, Value: "Rider"},
		{Kind: domain.RefField, Value: "qcomSkill"},
	}, qcom)
	assert.NotContains(t, qcom, domain.DocRef{Kind: domain.RefSchema, Value: "Riders"})

	endpoints := byHeading["Endpoints"].Refs
	assert.ElementsMatch(t, []domain.DocRef{
		{Kind: domain.RefOperation, Value: "allocation-service.allocate"},
		{Kind: domain.RefPath, Value: "/v1/riders/{riderId}/allocation"},
		{Kind: domain.RefPath, Value: "/v1/riders/{riderId}"},
		{Kind: domain.RefService, Value: "allocation-service"},
		{Kind: domain.RefService, Value: "rider-service"},
	}, endpoints)
	// The word "Rider" inside the fenced comment must not surface as a schema ref.
	for _, r := range endpoints {
		assert.NotEqual(t, domain.RefSchema, r.Kind)
	}
}

func TestGolden_Rider(t *testing.T) {
	known := docs.KnownRefs{
		Operations: []string{"rider-service.getRider"},
		Paths:      []string{"/v1/riders/{riderId}"},
		Aliases:    map[string]string{"GET /v1/riders/{riderId}": "rider-service.getRider"},
		Schemas:    []string{"Rider", "RiderProfile"},
		Concepts:   []string{"dispatch"},
		Services:   []string{"rider-service"},
	}
	o := docs.Options{ServiceID: "rider-service", Path: "docs/rider.md", Known: known}
	doc, err := docs.ParseFile("testdata/rider.md", o)
	require.NoError(t, err)

	require.Len(t, doc.Sections, 4)
	assert.Equal(t, "Rider Fields", doc.Title)

	byHeading := map[string]domain.DocSection{}
	for _, s := range doc.Sections {
		byHeading[s.Heading] = s
	}

	assert.ElementsMatch(t, []domain.DocRef{
		{Kind: domain.RefSchema, Value: "RiderProfile"},
		{Kind: domain.RefSchema, Value: "Rider"},
		{Kind: domain.RefField, Value: "qcomSkill"},
		{Kind: domain.RefField, Value: "upcoming_trips"},
		{Kind: domain.RefField, Value: "homeBase"},
	}, byHeading["Profile"].Refs)

	assert.ElementsMatch(t, []domain.DocRef{
		{Kind: domain.RefField, Value: "qcomSkill"},
		{Kind: domain.RefField, Value: "upcoming_trips"},
		{Kind: domain.RefField, Value: "homeBase"},
		{Kind: domain.RefConcept, Value: "dispatch"},
	}, byHeading["Field Reference"].Refs)

	assert.ElementsMatch(t, []domain.DocRef{
		{Kind: domain.RefOperation, Value: "rider-service.getRider"},
		{Kind: domain.RefSchema, Value: "Rider"},
		{Kind: domain.RefService, Value: "rider-service"},
	}, byHeading["Related Endpoints"].Refs)
}

func TestGolden_DispatchNotes(t *testing.T) {
	known := docs.KnownRefs{Concepts: []string{"dispatch"}}
	o := docs.Options{ServiceID: "allocation-service", Path: "docs/dispatch-notes.md", Known: known}
	doc, err := docs.ParseFile("testdata/dispatch-notes.md", o)
	require.NoError(t, err)

	assert.Equal(t, "dispatch-notes", doc.Title) // no H1, no explicit Title: falls back to file name

	require.Len(t, doc.Sections, 5)

	type want struct {
		heading string
		level   int
		suffix  string // ID suffix after "#"
	}
	wants := []want{
		{"dispatch-notes", 1, "dispatch-notes"},
		{"Dispatch Overview", 2, "dispatch-overview"},
		{"Examples", 3, "examples"},
		{"Troubleshooting", 2, "troubleshooting"},
		{"Examples", 3, "examples-2"},
	}
	for i, w := range wants {
		assert.Equal(t, w.heading, doc.Sections[i].Heading, "section %d heading", i)
		assert.Equal(t, w.level, doc.Sections[i].Level, "section %d level", i)
		assert.Equal(t, doc.ID+"#"+w.suffix, doc.Sections[i].ID, "section %d id", i)
		assert.Equal(t, i, doc.Sections[i].Ord, "section %d ord", i)
	}

	// The setext-looking "Dispatch Notes\n--------------" stays as body text,
	// not a separate section (setext headings are unsupported by design).
	assert.Contains(t, doc.Sections[1].Body, "Dispatch Notes\n--------------")

	// The YAML fence's "# this is a YAML comment" line must not have been
	// parsed as a heading, and no section titled from it should exist.
	for _, s := range doc.Sections {
		assert.NotContains(t, s.Heading, "YAML comment")
	}

	assert.Equal(t, []domain.DocRef{{Kind: domain.RefConcept, Value: "dispatch"}}, doc.Sections[0].Refs)
	assert.Equal(t, []domain.DocRef{{Kind: domain.RefConcept, Value: "dispatch"}}, doc.Sections[1].Refs)
	assert.Empty(t, doc.Sections[2].Refs)
	assert.Empty(t, doc.Sections[3].Refs)
	assert.Empty(t, doc.Sections[4].Refs)
}
