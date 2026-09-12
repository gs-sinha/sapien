package flow

import (
	"context"
	"reflect"
	"regexp"
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
	// Raised from 210 for "Setup and teardown" (PLAN §8/§9) and again for
	// "Soft assertions": still a budget, just one that fits the DSL's
	// current size rather than the size it had before those features.
	n := strings.Count(Reference(), "\n")
	assert.LessOrEqual(t, n, 280, "Reference() should stay a concise, agent-sized reference")
}

// TestReference_DocumentsEveryDSLKey pins the reference to the DSL's actual
// shape: every YAML key the parser accepts must appear in the text an agent
// reads. `soft:` shipped in the runner, the JSON schema, and docs/flows.md
// but never here, so agents writing flows over MCP could not find it and
// rediscovered it from a failed run; this test is what makes that
// impossible for the next key.
func TestReference_DocumentsEveryDSLKey(t *testing.T) {
	ref := Reference()
	for _, typ := range []reflect.Type{
		reflect.TypeOf(domain.Flow{}),
		reflect.TypeOf(domain.InputSpec{}),
		reflect.TypeOf(domain.Step{}),
		reflect.TypeOf(domain.ExplicitParams{}),
		reflect.TypeOf(domain.Poll{}),
		reflect.TypeOf(domain.Assertion{}),
		reflect.TypeOf(domain.Range{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			key := strings.Split(typ.Field(i).Tag.Get("yaml"), ",")[0]
			if key == "" || key == "-" {
				continue // loader-populated, not part of the file
			}
			found, err := regexp.MatchString(`\b`+regexp.QuoteMeta(key)+`\b`, ref)
			require.NoError(t, err)
			assert.True(t, found, "Reference() should document %s.%s (`%s:`); an agent that cannot find a key in the reference cannot use it", typ.Name(), typ.Field(i).Name, key)
		}
	}
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
