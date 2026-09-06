package flow

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// fencedYAMLBlocks extracts the content of every ```yaml ... ``` fenced
// code block in md.
func fencedYAMLBlocks(md string) []string {
	var blocks []string
	lines := strings.Split(md, "\n")
	var cur []string
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case !inBlock && trimmed == "```yaml":
			inBlock = true
			cur = nil
		case inBlock && trimmed == "```":
			inBlock = false
			blocks = append(blocks, strings.Join(cur, "\n")+"\n")
		case inBlock:
			cur = append(cur, line)
		}
	}
	return blocks
}

func TestReference_UnderLineLimit(t *testing.T) {
	// Raised from 210 to make room for the "Setup and teardown" section
	// (PLAN §8/§9): still a budget, just one that fits the DSL's current
	// size rather than its pre-setup/teardown one.
	n := strings.Count(Reference(), "\n")
	assert.LessOrEqual(t, n, 240, "Reference() should stay a concise, agent-sized reference")
}

func TestReference_HasTitleAndDiagnosticSection(t *testing.T) {
	ref := Reference()
	assert.Contains(t, ref, "# Flow DSL reference")
	assert.Contains(t, ref, "## Diagnostic codes")
	assert.Contains(t, ref, "## Reusing an example")
	for _, code := range []string{
		CodeFlowVersion, CodeNoSteps, CodeReservedKey, CodeUnknownKey, CodeSchema,
		CodeStepIDDuplicate, CodeStepIDInvalid, CodeUnknownOperation, CodeDeprecatedOperation,
		CodeUnknownInputName, CodeMissingRequiredParam, CodeMissingBody, CodeUnexpectedBody,
		CodeUnknownBodyField, CodeExprSyntax, CodeUnknownStep, CodeStepOrder,
		CodeUnknownFlowInput, CodeContextRoot, CodeSecretContext, CodeUnknownField,
		CodeAssertionInvalid, CodeDuplicateExtract, CodeInvalidDuration,
		CodeUnknownExample, CodeExampleOperationMismatch,
	} {
		assert.Contains(t, ref, code, "Reference() should document diagnostic code %s", code)
	}
}

// TestReference_FencedExamplesValidate validates every fenced ```yaml block
// against the fake catalog plus a fake resolver seeded with the
// "create-qcom-order" example the "Reusing an example" subsection's worked
// example names, so that block validates cleanly too.
func TestReference_FencedExamplesValidate(t *testing.T) {
	blocks := fencedYAMLBlocks(Reference())
	require.NotEmpty(t, blocks, "Reference() should contain at least one ```yaml example")

	resolver := newFakeResolver(domain.SavedExample{
		ID:        "create-qcom-order",
		Operation: "order-service.createOrder",
		Body: map[string]any{
			"customerId": "c1", "type": "QCOM",
			"pickup": map[string]any{"lat": 12.97, "lng": 77.59},
			"drop":   map[string]any{"lat": 12.93, "lng": 77.61},
		},
	})
	v := NewValidator(newFakeCatalog(), WithExampleResolver(resolver))
	for i, block := range blocks {
		f, res := v.ValidateSource(context.Background(), block)
		require.NotNil(t, res)
		assert.True(t, res.Valid, "example #%d should validate cleanly against the fake catalog: %+v\n---\n%s", i, res.Diagnostics, block)
		require.NotNil(t, f, "example #%d should parse", i)
	}
}
