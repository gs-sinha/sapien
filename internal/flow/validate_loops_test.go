package flow

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// validateFlow runs Validate() directly on a hand-built *domain.Flow,
// bypassing Parse/the JSON schema entirely -- used for BLOCK_SHAPE shapes
// the schema already rejects earlier (as SCHEMA/UNKNOWN_KEY) when a flow
// goes through YAML text, so the Go-level check underneath is still
// reachable and tested for a flow built programmatically (PLAN §34f.8).
func validateFlow(t *testing.T, f *domain.Flow) *domain.ValidationResult {
	t.Helper()
	v := NewValidator(newFakeCatalog())
	return v.Validate(context.Background(), f)
}

func allocateNestedStep(id, orderID string) domain.Step {
	return domain.Step{ID: id, Call: "allocation-service.allocate", Body: map[string]any{"orderId": orderID}}
}

// ---- BLOCK_SHAPE, NESTED_LOOP, LOOP_IN_PHASE (PLAN §34f.8) ----------------

func TestValidateSource_ForeachBlock_Valid(t *testing.T) {
	src := `version: 1
steps:
  - id: each
    foreach: "[1, 2, 3]"
    max: 10
    steps:
      - id: allocate
        call: allocation-service.allocate
        body: { orderId: "${iter.item}" }
        assert: [status == 201]
`
	_, res := validateSrc(t, src)
	assert.True(t, res.Valid, "%+v", res.Diagnostics)
}

func TestValidateSource_RepeatBlock_Valid(t *testing.T) {
	src := `version: 1
steps:
  - id: page
    repeat:
      until: "iter.index >= 2"
      max: 5
    steps:
      - id: allocate
        call: allocation-service.allocate
        body: { orderId: o1 }
        assert: [status == 201]
`
	_, res := validateSrc(t, src)
	assert.True(t, res.Valid, "%+v", res.Diagnostics)
}

func TestValidateSource_Block_NeitherForeachNorRepeat(t *testing.T) {
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "each", Max: 10, Steps: []domain.Step{allocateNestedStep("allocate", "o1")},
	}}}
	res := validateFlow(t, f)
	d := diag(res, CodeBlockShape)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_Block_BothForeachAndRepeat(t *testing.T) {
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "each", Foreach: "[1]", Repeat: &domain.Repeat{Until: "true", Max: 2},
		Steps: []domain.Step{allocateNestedStep("allocate", "o1")},
	}}}
	res := validateFlow(t, f)
	d := diag(res, CodeBlockShape)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_Block_CallOnlyFieldSet(t *testing.T) {
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "each", Foreach: "[1]", Body: map[string]any{"x": 1},
		Steps: []domain.Step{allocateNestedStep("allocate", "o1")},
	}}}
	res := validateFlow(t, f)
	d := diag(res, CodeBlockShape)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_Block_CallSet(t *testing.T) {
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "each", Call: "allocation-service.allocate", Foreach: "[1]",
		Steps: []domain.Step{allocateNestedStep("allocate", "o1")},
	}}}
	res := validateFlow(t, f)
	d := diag(res, CodeBlockShape)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_CallStep_BlockOnlyFieldSet(t *testing.T) {
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "a", Call: "allocation-service.allocate", BreakWhen: "true", Body: map[string]any{"orderId": "o1"},
	}}}
	res := validateFlow(t, f)
	d := diag(res, CodeBlockShape)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_Repeat_MissingMax(t *testing.T) {
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "each", Repeat: &domain.Repeat{Until: "true"},
		Steps: []domain.Step{allocateNestedStep("allocate", "o1")},
	}}}
	res := validateFlow(t, f)
	d := diag(res, CodeBlockShape)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_Repeat_MissingUntilAndWhile(t *testing.T) {
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "each", Repeat: &domain.Repeat{Max: 5},
		Steps: []domain.Step{allocateNestedStep("allocate", "o1")},
	}}}
	res := validateFlow(t, f)
	d := diag(res, CodeBlockShape)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_Repeat_MaxOutOfRange(t *testing.T) {
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "each", Repeat: &domain.Repeat{Until: "true", Max: 5000},
		Steps: []domain.Step{allocateNestedStep("allocate", "o1")},
	}}}
	res := validateFlow(t, f)
	d := diag(res, CodeBlockShape)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_Foreach_MaxOutOfRange(t *testing.T) {
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "each", Foreach: "[1]", Max: 5000,
		Steps: []domain.Step{allocateNestedStep("allocate", "o1")},
	}}}
	res := validateFlow(t, f)
	d := diag(res, CodeBlockShape)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

// TestValidateSource_Block_ShapeErrors_CaughtByYAMLSchema confirms the
// common shape mistakes are rejected even earlier, at the schema level
// (SCHEMA/UNKNOWN_KEY), for a flow read from YAML -- checkBlockShape (see
// the Validate()-direct tests above) is the defense-in-depth layer for a
// flow built programmatically, bypassing the schema entirely.
func TestValidateSource_Block_ShapeErrors_CaughtByYAMLSchema(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"neither foreach nor repeat", "version: 1\nsteps:\n  - id: e\n    max: 10\n    steps:\n      - id: a\n        call: allocation-service.allocate\n        body: {orderId: o1}\n"},
		{"both foreach and repeat", "version: 1\nsteps:\n  - id: e\n    foreach: \"[1]\"\n    repeat: {until: \"true\", max: 2}\n    steps:\n      - id: a\n        call: allocation-service.allocate\n        body: {orderId: o1}\n"},
		{"call-only field on block", "version: 1\nsteps:\n  - id: e\n    foreach: \"[1]\"\n    body: {x: 1}\n    steps:\n      - id: a\n        call: allocation-service.allocate\n        body: {orderId: o1}\n"},
		{"call set on block", "version: 1\nsteps:\n  - id: e\n    call: allocation-service.allocate\n    foreach: \"[1]\"\n    steps:\n      - id: a\n        call: allocation-service.allocate\n        body: {orderId: o1}\n"},
		{"block-only field on call step", "version: 1\nsteps:\n  - id: a\n    call: allocation-service.allocate\n    break_when: \"true\"\n    body: {orderId: o1}\n"},
		{"repeat missing max", "version: 1\nsteps:\n  - id: e\n    repeat: {until: \"true\"}\n    steps:\n      - id: a\n        call: allocation-service.allocate\n        body: {orderId: o1}\n"},
		{"repeat missing until/while", "version: 1\nsteps:\n  - id: e\n    repeat: {max: 5}\n    steps:\n      - id: a\n        call: allocation-service.allocate\n        body: {orderId: o1}\n"},
		{"repeat max out of range", "version: 1\nsteps:\n  - id: e\n    repeat: {until: \"true\", max: 5000}\n    steps:\n      - id: a\n        call: allocation-service.allocate\n        body: {orderId: o1}\n"},
		{"foreach max out of range", "version: 1\nsteps:\n  - id: e\n    foreach: \"[1]\"\n    max: 5000\n    steps:\n      - id: a\n        call: allocation-service.allocate\n        body: {orderId: o1}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, res := validateSrc(t, tc.src)
			require.False(t, res.Valid, "%+v", res.Diagnostics)
			require.NotEmpty(t, res.Diagnostics)
		})
	}
}

func TestValidateSource_NestedLoop(t *testing.T) {
	src := `version: 1
steps:
  - id: outer
    foreach: "[1]"
    steps:
      - id: inner
        foreach: "[2]"
        steps:
          - id: allocate
            call: allocation-service.allocate
            body: { orderId: o1 }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeNestedLoop)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Equal(t, "inner", d.StepID)
	assert.False(t, res.Valid)
}

func TestValidateSource_LoopInSetup(t *testing.T) {
	src := `version: 1
setup:
  - id: each
    foreach: "[1]"
    steps:
      - id: allocate
        call: allocation-service.allocate
        body: { orderId: o1 }
steps:
  - id: rider
    call: rider-service.getRider
    input: { riderId: R1 }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeLoopInPhase)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_LoopInTeardown(t *testing.T) {
	src := `version: 1
steps:
  - id: rider
    call: rider-service.getRider
    input: { riderId: R1 }
teardown:
  - id: each
    foreach: "[1]"
    steps:
      - id: allocate
        call: allocation-service.allocate
        body: { orderId: o1 }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeLoopInPhase)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

// ---- duplicate ids across nesting -----------------------------------------

func TestValidateSource_DuplicateStepID_NestedVsTopLevel(t *testing.T) {
	src := `version: 1
steps:
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: o1 }
  - id: each
    foreach: "[1]"
    steps:
      - id: allocate
        call: allocation-service.allocate
        body: { orderId: o2 }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeStepIDDuplicate)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

// ---- relaxed step order inside a block ------------------------------------

// TestValidateSource_Block_SiblingForwardReference_NoStepOrderError confirms
// a nested step may reference a LATER sibling in its own block without a
// STEP_ORDER error (steps.<id> = latest execution; on iteration N this
// reads iteration N-1's value of the later-declared sibling).
func TestValidateSource_Block_SiblingForwardReference_NoStepOrderError(t *testing.T) {
	src := `version: 1
steps:
  - id: page
    repeat:
      until: "iter.index >= 2"
      max: 5
    steps:
      - id: a
        call: allocation-service.allocate
        body: { orderId: "${has(steps.b) ? steps.b.out.x : 'o1'}" }
        extract: { x: body.orderId }
      - id: b
        call: allocation-service.allocate
        body: { orderId: "${steps.a.out.x}" }
        extract: { x: body.orderId }
`
	_, res := validateSrc(t, src)
	assert.Nil(t, diag(res, CodeStepOrder), "%+v", res.Diagnostics)
	assert.True(t, res.Valid, "%+v", res.Diagnostics)
}

// TestValidateSource_Block_SiblingForwardReference_MaybeSkippedWhenUnguarded
// confirms the SAME forward reference, unguarded, is a MAYBE_SKIPPED
// warning (not an error): on the first iteration the later sibling hasn't
// run yet.
func TestValidateSource_Block_SiblingForwardReference_MaybeSkippedWhenUnguarded(t *testing.T) {
	src := `version: 1
steps:
  - id: page
    repeat:
      until: "iter.index >= 2"
      max: 5
    steps:
      - id: a
        call: allocation-service.allocate
        body: { orderId: "${steps.b.out.x}" }
      - id: b
        call: allocation-service.allocate
        body: { orderId: "${steps.a.out.x}" }
        extract: { x: body.orderId }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeMaybeSkipped)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Equal(t, "a", d.StepID)
	assert.True(t, res.Valid, "MAYBE_SKIPPED is a warning: %+v", res.Diagnostics)
}

// TestValidateSource_Block_ReferenceOutsideBlock_StillStepOrder confirms the
// relaxation is scoped to the SAME block: a step outside any block, or in a
// different block, referencing a nested step forward still gets STEP_ORDER.
func TestValidateSource_Block_ReferenceOutsideBlock_StillStepOrder(t *testing.T) {
	src := `version: 1
steps:
  - id: before
    call: allocation-service.allocate
    body: { orderId: "${steps.nested.out.x}" }
  - id: each
    foreach: "[1]"
    steps:
      - id: nested
        call: allocation-service.allocate
        body: { orderId: o1 }
        extract: { x: body.orderId }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeStepOrder)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

// TestValidateSource_Block_ReferenceAfterBlock_ForwardOK confirms a step
// AFTER the block may reference a nested step's (latest) value normally,
// like any other earlier step -- MAYBE_SKIPPED still applies since the
// block may have run zero iterations.
func TestValidateSource_Block_ReferenceAfterBlock_ForwardOK(t *testing.T) {
	src := `version: 1
steps:
  - id: each
    foreach: "[1]"
    steps:
      - id: nested
        call: allocation-service.allocate
        body: { orderId: o1 }
        extract: { x: body.orderId }
  - id: after
    call: allocation-service.allocate
    body: { orderId: "${has(steps.nested) ? steps.nested.out.x : 'o1'}" }
`
	_, res := validateSrc(t, src)
	assert.Nil(t, diag(res, CodeStepOrder), "%+v", res.Diagnostics)
	assert.Nil(t, diag(res, CodeMaybeSkipped), "%+v", res.Diagnostics)
	assert.True(t, res.Valid, "%+v", res.Diagnostics)
}

// ---- `iter` root availability ----------------------------------------------

func TestValidateSource_IterOutsideBlock_ContextRoot(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: allocation-service.allocate
    body: { orderId: "${iter.item}" }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeContextRoot)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_IterInForeach_ContextRoot(t *testing.T) {
	// `iter` isn't available in the block's own `foreach` expression
	// (evaluated once, before any iteration).
	src := `version: 1
steps:
  - id: each
    foreach: "[iter.index]"
    steps:
      - id: allocate
        call: allocation-service.allocate
        body: { orderId: o1 }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeContextRoot)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_IterInBreakWhen_Valid(t *testing.T) {
	src := `version: 1
steps:
  - id: each
    foreach: "[1, 2, 3]"
    break_when: "iter.index >= 1"
    steps:
      - id: allocate
        call: allocation-service.allocate
        body: { orderId: "${iter.item}" }
`
	_, res := validateSrc(t, src)
	assert.True(t, res.Valid, "%+v", res.Diagnostics)
}

// ---- `when` on a block and a nested step -----------------------------------

func TestValidateSource_WhenOnBlock_Valid(t *testing.T) {
	src := `version: 1
inputs:
  run: { type: boolean, default: false }
steps:
  - id: each
    when: inputs.run
    foreach: "[1]"
    steps:
      - id: allocate
        call: allocation-service.allocate
        body: { orderId: o1 }
`
	_, res := validateSrc(t, src)
	assert.True(t, res.Valid, "%+v", res.Diagnostics)
}

func TestValidateSource_WhenOnNestedStep_Valid(t *testing.T) {
	src := `version: 1
steps:
  - id: each
    foreach: "[1, 2]"
    steps:
      - id: allocate
        when: "iter.index == 0"
        call: allocation-service.allocate
        body: { orderId: "${iter.item}" }
`
	_, res := validateSrc(t, src)
	assert.True(t, res.Valid, "%+v", res.Diagnostics)
}

// ---- repeat.interval duration check ----------------------------------------

func TestValidate_RepeatIntervalInvalidDuration(t *testing.T) {
	// "bogus" also fails the schema's duration pattern when going through
	// YAML; checked here directly against Validate() so INVALID_DURATION
	// itself (not just SCHEMA) is exercised, the same way
	// TestValidateSource_Repeat_MaxOutOfRange checks BLOCK_SHAPE.
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "each", Repeat: &domain.Repeat{Until: "true", Max: 5, Interval: "bogus"},
		Steps: []domain.Step{allocateNestedStep("allocate", "o1")},
	}}}
	res := validateFlow(t, f)
	d := diag(res, CodeInvalidDuration)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

// ---- schema (Parse-level) coverage for blocks ------------------------------

func TestParse_ForeachAndRepeatNoLongerReserved(t *testing.T) {
	f, err := Parse(`version: 1
steps:
  - id: each
    foreach: "[1]"
    max: 10
    break_when: "iter.index > 0"
    on_error: continue
    steps:
      - id: a
        call: allocation-service.allocate
        body: { orderId: "${iter.item}" }
`)
	require.NoError(t, err)
	require.Len(t, f.Steps, 1)
	block := f.Steps[0]
	assert.Equal(t, "[1]", block.Foreach)
	assert.Equal(t, 10, block.Max)
	assert.Equal(t, "iter.index > 0", block.BreakWhen)
	assert.Equal(t, "continue", block.OnError)
	require.Len(t, block.Steps, 1)
	assert.Equal(t, "a", block.Steps[0].ID)
	assert.True(t, block.IsBlock())
	assert.False(t, block.Steps[0].IsBlock())
}

func TestParse_RepeatBlock(t *testing.T) {
	f, err := Parse(`version: 1
steps:
  - id: page
    repeat:
      until: "iter.index >= 3"
      max: 5
      interval: 10ms
    steps:
      - id: a
        call: allocation-service.allocate
        body: { orderId: o1 }
`)
	require.NoError(t, err)
	require.Len(t, f.Steps, 1)
	require.NotNil(t, f.Steps[0].Repeat)
	assert.Equal(t, "iter.index >= 3", f.Steps[0].Repeat.Until)
	assert.Equal(t, 5, f.Steps[0].Repeat.Max)
	assert.Equal(t, "10ms", f.Steps[0].Repeat.Interval)
}

func TestParse_ParallelStillReserved(t *testing.T) {
	src := readExample(t, "flow-reserved-parallel.invalid.yaml")
	_, err := Parse(src)
	require.Error(t, err)
	diags := diagnosticsOf(t, err)
	found := findCode(diags, CodeReservedKey)
	require.NotNil(t, found, "%+v", diags)
}

// TestValidateSource_Block_EarlierSiblingWithoutWhen_NeedsNoGuard: inside
// one iteration, an earlier sibling that has no `when` of its own has always
// run by the time a later sibling reads it, so that reference is not a
// MAYBE_SKIPPED -- while the same read of a sibling that DOES carry a `when`
// still is.
func TestValidateSource_Block_EarlierSiblingWithoutWhen_NeedsNoGuard(t *testing.T) {
	src := `version: 1
inputs:
  ids: { type: array, required: true }
steps:
  - id: each
    foreach: inputs.ids
    steps:
      - id: a
        call: allocation-service.allocate
        body: { orderId: "${iter.item}" }
        extract: { x: body.orderId }
      - id: b
        call: allocation-service.allocate
        body: { orderId: "${steps.a.out.x}" }
`
	_, res := validateSrc(t, src)
	assert.Nil(t, diag(res, CodeMaybeSkipped), "%+v", res.Diagnostics)

	guarded := strings.Replace(src, "      - id: a\n", "      - id: a\n        when: \"iter.index > 0\"\n", 1)
	_, res = validateSrc(t, guarded)
	d := diag(res, CodeMaybeSkipped)
	require.NotNil(t, d, "a sibling with its own `when` may be skipped: %+v", res.Diagnostics)
	assert.Equal(t, "b", d.StepID)
}
