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

func TestReference_UnknownTopicListsService(t *testing.T) {
	f := &flowAPI{}
	_, err := f.Reference(context.Background(), "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service")
}
