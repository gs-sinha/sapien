package local

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReference_Service(t *testing.T) {
	f := &flowAPI{}
	text, err := f.Reference(context.Background(), "service")
	require.NoError(t, err)
	assert.Equal(t, serviceReferenceText, text)
	for _, want := range []string{
		"api/openapi.yaml", "service.yaml", "docs/", "operationId",
		"add_service", "sapien service add", "Onboarding checklist",
		"## Keeping docs current", "CLAUDE.md", "AGENTS.md", "in the same change",
		"## Warnings", "accepted_warnings", "STALE_ACCEPTANCE",
		"Never silence a warning by misdescribing the wire",
		"sync_service", "re-reads",
	} {
		assert.Contains(t, text, want)
	}
}

// TestReference_Service_BusinessLogicAndInterview pins the three things the
// "docs are lacking" feedback from consuming agents asked for: docs written
// for an agent in another repo rather than a reviewer of this one, an
// interview step for the knowledge that is in nobody's code, and a request
// example on every body so the next caller is not compiling payloads out of a
// field list.
func TestReference_Service_BusinessLogicAndInterview(t *testing.T) {
	f := &flowAPI{}
	text, err := f.Reference(context.Background(), "service")
	require.NoError(t, err)

	// The audience, stated before the format.
	assert.Contains(t, text, "## Who reads what you write")
	assert.Contains(t, text, "different")
	assert.Contains(t, text, "restate the contract")

	// Documentation depth: the questions a section must answer.
	for _, want := range []string{
		"overview.md", "Preconditions", "idempoten", "What it changes",
		"Every error code", "Deprecations", "Open questions",
	} {
		assert.Contains(t, text, want)
	}

	// The interview: after drafting, batched, and only for what code cannot say.
	idx := strings.Index(text, "## Interview the owner")
	require.Greater(t, idx, 0, "expected an \"## Interview the owner\" section")
	interview := text[idx:]
	assert.Contains(t, interview, "one round")
	assert.Contains(t, interview, "after you have drafted")
	assert.Contains(t, interview, "Only ask what the code cannot tell you")
	assert.Contains(t, interview, "create_memory")

	// Examples: the contract is the authored home, a run is what verifies one.
	assert.Contains(t, text, "example:")
	assert.Contains(t, text, "create_example(run_id=...)")
	assert.Contains(t, text, "request_example")

	// Coverage: the numbers and every code that names a gap.
	cidx := strings.Index(text, "## Coverage")
	require.Greater(t, cidx, 0, "expected a \"## Coverage\" section")
	for _, code := range []string{
		"NO_NARRATIVE_DOCS", "UNDOCUMENTED_OPERATION", "MISSING_REQUEST_EXAMPLE", "NO_CONCEPTS",
	} {
		assert.Contains(t, text[cidx:], code)
	}
}

// TestReference_Service_WarningsSection checks the "Warnings" section
// specifically: it must state the acceptance rule, show the accepted_warnings
// YAML shape with its required/optional fields, and explain STALE_ACCEPTANCE
// -- the three things the checklist step points back to.
func TestReference_Service_WarningsSection(t *testing.T) {
	f := &flowAPI{}
	text, err := f.Reference(context.Background(), "service")
	require.NoError(t, err)

	idx := strings.Index(text, "## Warnings")
	require.Greater(t, idx, 0, "expected a \"## Warnings\" section")
	section := text[idx:]

	for _, want := range []string{
		"lint",
		"code: UNSUPPORTED_MEDIA_TYPE",
		"match:",
		"reason:",
		"required, non-empty",
		"STALE_ACCEPTANCE",
		"remove",
		"sync_service",
	} {
		assert.Contains(t, section, want)
	}

	// The checklist step 4 must point back at accepting warnings, not just
	// "resolve every warning".
	assert.Contains(t, text, "accept")
	assert.NotContains(t, text, "Resolve every warning it reports")
}

// TestReference_Sapien pins the orientation topic: an agent that has never
// used Sapien must learn from it what Sapien is for, how to consume a service
// it does not own, why to call through execute_api instead of its own script,
// and where onboarding is documented. Agents were using the tools without any
// of that framing, which is how schemas got read here and calls got made
// elsewhere.
func TestReference_Sapien(t *testing.T) {
	f := &flowAPI{}
	text, err := f.Reference(context.Background(), "sapien")
	require.NoError(t, err)
	assert.Equal(t, sapienReferenceText, text)
	for _, want := range []string{
		"get_context", "search_apis", "get_api", "request_example",
		"search_docs", "get_relevant_memories", "list_examples", "execute_api",
		"create_example(run_id)", `get_dsl_reference("service")`,
		`get_dsl_reference("flow")`, "another repository",
	} {
		assert.Contains(t, text, want)
	}
}

func TestReference_UnknownTopicListsService(t *testing.T) {
	f := &flowAPI{}
	_, err := f.Reference(context.Background(), "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service")
}
