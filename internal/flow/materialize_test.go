package flow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// createQcomExample mirrors PLAN §34b's own worked example: a saved,
// verified request for order-service.createOrder.
func createQcomExample() domain.SavedExample {
	return domain.SavedExample{
		ID:        "create-qcom-order",
		Operation: "order-service.createOrder",
		Body: map[string]any{
			"customerId": "c1",
			"type":       "QCOM",
			"pickup":     map[string]any{"lat": 12.97, "lng": 77.59},
			"drop":       map[string]any{"lat": 12.93, "lng": 77.61},
		},
		Headers: map[string]string{"X-Trace-Source": "example"},
	}
}

func TestMaterialize_NilFlow(t *testing.T) {
	f, diags := Materialize(context.Background(), nil, nil)
	assert.Nil(t, f)
	assert.Nil(t, diags)
}

func TestMaterialize_NoExampleStepsUntouched(t *testing.T) {
	f := &domain.Flow{Version: 1, Steps: []domain.Step{
		{ID: "a", Call: "order-service.createOrder", Body: map[string]any{"x": 1}},
	}}
	out, diags := Materialize(context.Background(), f, newFakeResolver())
	assert.Empty(t, diags)
	require.Len(t, out.Steps, 1)
	assert.Equal(t, "order-service.createOrder", out.Steps[0].Call)
	assert.Equal(t, map[string]any{"x": 1}, out.Steps[0].Body)
	// f itself is not mutated.
	assert.Equal(t, f.Steps[0].Body, out.Steps[0].Body)
}

func TestMaterialize_FillsCallInputBodyHeaders(t *testing.T) {
	ex := createQcomExample()
	ex.Input = map[string]any{"city": "Bengaluru"}
	r := newFakeResolver(ex)

	f := &domain.Flow{Version: 1, Steps: []domain.Step{{ID: "create", Example: "create-qcom-order"}}}
	out, diags := Materialize(context.Background(), f, r)
	require.Empty(t, diags)
	require.Len(t, out.Steps, 1)
	st := out.Steps[0]
	assert.Equal(t, "create-qcom-order", st.Example, "Example is kept for display")
	assert.Equal(t, "order-service.createOrder", st.Call)
	assert.Equal(t, map[string]any{"city": "Bengaluru"}, st.Input)
	assert.Equal(t, ex.Body, st.Body)
	assert.Equal(t, map[string]string{"X-Trace-Source": "example"}, st.Headers)
}

func TestMaterialize_UnknownExample_WithSuggestion(t *testing.T) {
	r := newFakeResolver(createQcomExample())
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{ID: "a", Example: "create-qcom-ordr", Line: 4}}}
	out, diags := Materialize(context.Background(), f, r)
	require.Len(t, diags, 1)
	d := diags[0]
	assert.Equal(t, CodeUnknownExample, d.Code)
	assert.Equal(t, domain.SeverityError, d.Severity)
	assert.Equal(t, 4, d.Line)
	assert.Equal(t, "a", d.StepID)
	assert.Contains(t, d.Message, "create-qcom-ordr")
	require.NotEmpty(t, d.Suggestions)
	assert.Equal(t, "create-qcom-order", d.Suggestions[0])
	assert.Contains(t, d.Message, "did you mean `create-qcom-order`")
	// The step is returned unchanged: still unresolved, no call filled in.
	assert.Equal(t, "", out.Steps[0].Call)
}

func TestMaterialize_NilResolver_IsUnknownExample(t *testing.T) {
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{ID: "a", Example: "anything"}}}
	out, diags := Materialize(context.Background(), f, nil)
	require.Len(t, diags, 1)
	assert.Equal(t, CodeUnknownExample, diags[0].Code)
	assert.Empty(t, diags[0].Suggestions, "nil resolver has no candidates to rank")
	assert.Equal(t, "", out.Steps[0].Call)
}

func TestMaterialize_CallMatchesExample_NoDiagnostic(t *testing.T) {
	r := newFakeResolver(createQcomExample())
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "create", Call: "order-service.createOrder", Example: "create-qcom-order",
	}}}
	out, diags := Materialize(context.Background(), f, r)
	assert.Empty(t, diags)
	assert.Equal(t, "order-service.createOrder", out.Steps[0].Call)
}

func TestMaterialize_CallMismatchesExample(t *testing.T) {
	r := newFakeResolver(createQcomExample())
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "create", Call: "allocation-service.allocate", Example: "create-qcom-order", Line: 7,
	}}}
	out, diags := Materialize(context.Background(), f, r)
	require.Len(t, diags, 1)
	d := diags[0]
	assert.Equal(t, CodeExampleOperationMismatch, d.Code)
	assert.Equal(t, domain.SeverityError, d.Severity)
	assert.Equal(t, 7, d.Line)
	assert.Equal(t, "create", d.StepID)
	assert.Contains(t, d.Message, "allocation-service.allocate")
	assert.Contains(t, d.Message, "order-service.createOrder")
	// Explicit `call` wins even on a mismatch.
	assert.Equal(t, "allocation-service.allocate", out.Steps[0].Call)
}

// ---- merge precedence per field -------------------------------------------

func TestMaterialize_InputOverridePerKey(t *testing.T) {
	ex := createQcomExample()
	ex.Input = map[string]any{"city": "Bengaluru", "priority": "low"}
	r := newFakeResolver(ex)

	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "create", Example: "create-qcom-order",
		Input: map[string]any{"city": "Mumbai"}, // overrides just this key
	}}}
	out, _ := Materialize(context.Background(), f, r)
	assert.Equal(t, map[string]any{"city": "Mumbai", "priority": "low"}, out.Steps[0].Input)
}

func TestMaterialize_BodyReplacesEntirely(t *testing.T) {
	r := newFakeResolver(createQcomExample())
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "create", Example: "create-qcom-order",
		Body: map[string]any{"customerId": "c2"}, // replaces the whole body, not just this key
	}}}
	out, _ := Materialize(context.Background(), f, r)
	assert.Equal(t, map[string]any{"customerId": "c2"}, out.Steps[0].Body)
}

func TestMaterialize_HeadersMergeStepWins(t *testing.T) {
	r := newFakeResolver(createQcomExample()) // Headers: {X-Trace-Source: example}
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "create", Example: "create-qcom-order",
		Headers: map[string]string{"X-Trace-Source": "step", "X-Extra": "1"},
	}}}
	out, _ := Materialize(context.Background(), f, r)
	assert.Equal(t, map[string]string{"X-Trace-Source": "step", "X-Extra": "1"}, out.Steps[0].Headers)
}

// TestMaterialize_TemplatesPreserved checks that a `${...}` template inside
// the example's own fields survives materialization untouched (interpolated
// at run time, not here) -- both as a raw value and buried in the merged
// map alongside a step-level override.
func TestMaterialize_TemplatesPreserved(t *testing.T) {
	ex := domain.SavedExample{
		ID:        "templated",
		Operation: "order-service.createOrder",
		Body: map[string]any{
			"customerId": "${inputs.customerId}",
			"type":       "QCOM",
		},
		Headers: map[string]string{"X-Trace-Id": "${inputs.traceId}"},
	}
	r := newFakeResolver(ex)
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{ID: "create", Example: "templated"}}}
	out, diags := Materialize(context.Background(), f, r)
	require.Empty(t, diags)
	body := out.Steps[0].Body.(map[string]any)
	assert.Equal(t, "${inputs.customerId}", body["customerId"])
	assert.Equal(t, "${inputs.traceId}", out.Steps[0].Headers["X-Trace-Id"])
}

// ---- Validate/ValidateSource with an ExampleResolver -----------------------

func TestValidate_ExampleResolved_ValidatesLikeAnExplicitCall(t *testing.T) {
	ex := createQcomExample()
	v := NewValidator(newFakeCatalog(), WithExampleResolver(newFakeResolver(ex)))
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{ID: "create", Example: "create-qcom-order"}}}
	res := v.Validate(context.Background(), f)
	assert.True(t, res.Valid, "%+v", res.Diagnostics)
	assert.Empty(t, res.Diagnostics)
}

func TestValidate_UnknownExample(t *testing.T) {
	v := NewValidator(newFakeCatalog(), WithExampleResolver(newFakeResolver(createQcomExample())))
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{ID: "a", Example: "nope-at-all"}}}
	res := v.Validate(context.Background(), f)
	require.False(t, res.Valid)
	d := findCode(res.Diagnostics, CodeUnknownExample)
	require.NotNil(t, d, "%+v", res.Diagnostics)
}

func TestValidate_ExampleOperationMismatch(t *testing.T) {
	v := NewValidator(newFakeCatalog(), WithExampleResolver(newFakeResolver(createQcomExample())))
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "a", Call: "rider-service.getRider", Example: "create-qcom-order",
	}}}
	res := v.Validate(context.Background(), f)
	require.False(t, res.Valid)
	d := findCode(res.Diagnostics, CodeExampleOperationMismatch)
	require.NotNil(t, d, "%+v", res.Diagnostics)
}

func TestValidate_ExampleWithoutResolver_IsUnknownExample(t *testing.T) {
	v := NewValidator(newFakeCatalog()) // no WithExampleResolver
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{ID: "a", Example: "create-qcom-order"}}}
	res := v.Validate(context.Background(), f)
	require.False(t, res.Valid)
	d := findCode(res.Diagnostics, CodeUnknownExample)
	require.NotNil(t, d, "%+v", res.Diagnostics)
}

func TestValidate_NeitherCallNorExample(t *testing.T) {
	v := NewValidator(newFakeCatalog())
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{ID: "a", Line: 3}}}
	res := v.Validate(context.Background(), f)
	require.False(t, res.Valid)
	d := diag(res, CodeSchema)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Contains(t, d.Message, "`call`")
	assert.Contains(t, d.Message, "`example`")
	assert.Equal(t, "a", d.StepID)
	assert.Equal(t, 3, d.Line)
	// Only one diagnostic for this step: no bogus "unknown operation ``"
	// alongside it.
	assert.Len(t, res.Diagnostics, 1)
}

func TestValidateSource_NeitherCallNorExample_SchemaLevel(t *testing.T) {
	src := "version: 1\nsteps:\n  - id: a\n"
	_, res := validateSrc(t, src)
	require.False(t, res.Valid)
	require.Len(t, res.Diagnostics, 1, "the two anyOf branch failures merge into one diagnostic: %+v", res.Diagnostics)
	d := res.Diagnostics[0]
	assert.Equal(t, CodeSchema, d.Code)
	assert.Contains(t, d.Message, "`call`")
	assert.Contains(t, d.Message, "`example`")
}

func TestValidateSource_ExampleAndCallBothGiven_MatchingIsFine(t *testing.T) {
	ex := createQcomExample()
	v := NewValidator(newFakeCatalog(), WithExampleResolver(newFakeResolver(ex)))
	src := "version: 1\nsteps:\n  - id: create\n    call: order-service.createOrder\n    example: create-qcom-order\n"
	f, res := v.ValidateSource(context.Background(), src)
	require.True(t, res.Valid, "%+v", res.Diagnostics)
	require.NotNil(t, f)
	assert.Equal(t, ex.Body, f.Steps[0].Body, "ValidateSource returns the materialized flow")
}

func TestValidateSource_ReturnsMaterializedFlow(t *testing.T) {
	ex := createQcomExample()
	v := NewValidator(newFakeCatalog(), WithExampleResolver(newFakeResolver(ex)))
	src := "version: 1\nsteps:\n  - id: create\n    example: create-qcom-order\n"
	f, res := v.ValidateSource(context.Background(), src)
	require.True(t, res.Valid, "%+v", res.Diagnostics)
	require.NotNil(t, f)
	assert.Equal(t, "order-service.createOrder", f.Steps[0].Call)
	assert.Equal(t, ex.Body, f.Steps[0].Body)
	assert.Equal(t, "create-qcom-order", f.Steps[0].Example)
}
