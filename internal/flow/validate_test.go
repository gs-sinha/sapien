package flow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

func validateSrc(t *testing.T, src string) (*domain.Flow, *domain.ValidationResult) {
	t.Helper()
	v := NewValidator(newFakeCatalog())
	f, res := v.ValidateSource(context.Background(), src)
	require.NotNil(t, res, "ValidateSource must never return a nil result")
	return f, res
}

func diag(res *domain.ValidationResult, code string) *domain.Diagnostic {
	return findCode(res.Diagnostics, code)
}

// baseFlow is a minimal, fully-valid flow (against the fake catalog) that
// each diagnostic test tweaks in one specific way.
const baseFlowTemplate = `version: 1
id: t
inputs:
  orderId: { type: string, required: true }
steps:
  - id: create
    call: order-service.createOrder
    body:
      customerId: cust_1
      type: QCOM
      pickup: { lat: 1, lng: 2 }
      drop: { lat: 3, lng: 4 }
    extract:
      orderId: body.orderId
    assert:
      - status == 201
  - id: rider
    call: rider-service.getRider
    input:
      riderId: R1
    assert:
      - status == 200
`

func TestValidateSource_BaseFlowIsValid(t *testing.T) {
	_, res := validateSrc(t, baseFlowTemplate)
	assert.True(t, res.Valid, "%+v", res.Diagnostics)
}

func TestValidate_FlowVersion(t *testing.T) {
	v := NewValidator(newFakeCatalog())
	f := &domain.Flow{Version: 2, Steps: []domain.Step{{ID: "a", Call: "order-service.createOrder"}}}
	res := v.Validate(context.Background(), f)
	require.NotNil(t, diag(res, CodeFlowVersion))
	assert.False(t, res.Valid)
}

func TestValidate_NoSteps(t *testing.T) {
	v := NewValidator(newFakeCatalog())
	f := &domain.Flow{Version: 1}
	res := v.Validate(context.Background(), f)
	require.NotNil(t, diag(res, CodeNoSteps))
	assert.False(t, res.Valid)
}

func TestValidateSource_StepIDDuplicate(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: order-service.createOrder
  - id: a
    call: allocation-service.allocate
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeStepIDDuplicate)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Equal(t, "a", d.StepID)
	assert.False(t, res.Valid)
}

func TestValidate_StepIDInvalid(t *testing.T) {
	// The schema itself enforces the id pattern, so this code is only
	// reachable directly against a hand-built domain.Flow that bypassed it.
	v := NewValidator(newFakeCatalog())
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{ID: "1bad", Call: "order-service.createOrder"}}}
	res := v.Validate(context.Background(), f)
	d := diag(res, CodeStepIDInvalid)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_UnknownOperation(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: order-service.bogusOp
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeUnknownOperation)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.NotEmpty(t, d.Suggestions)
	assert.False(t, res.Valid)
}

func TestValidateSource_DeprecatedOperation(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: allocation-service.allocateV1
    body: { orderId: ord_1 }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeDeprecatedOperation)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Equal(t, domain.SeverityWarning, d.Severity)
	assert.True(t, res.Valid, "a warning alone must not make a flow invalid: %+v", res.Diagnostics)
}

func TestValidateSource_UnknownInputName(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: rider-service.getRider
    input:
      bogusName: R1
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeUnknownInputName)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Contains(t, d.Suggestions, "riderId (path)")
	assert.False(t, res.Valid)
}

func TestValidateSource_UnknownInputName_ParamsLocation(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: rider-service.getRider
    params:
      query:
        bogus: x
`
	_, res := validateSrc(t, src)
	require.NotNil(t, diag(res, CodeUnknownInputName), "%+v", res.Diagnostics)
}

func TestValidateSource_MissingRequiredParam(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: rider-service.getRider
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeMissingRequiredParam)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_MissingBody(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: allocation-service.allocate
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeMissingBody)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_UnexpectedBody(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: rider-service.getRider
    input: { riderId: R1 }
    body: { bogus: 1 }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeUnexpectedBody)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Equal(t, domain.SeverityWarning, d.Severity)
	assert.True(t, res.Valid)
}

func TestValidateSource_UnknownBodyField(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: allocation-service.allocate
    body: { orderId: ord_1, extra: 1 }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeUnknownBodyField)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Equal(t, domain.SeverityWarning, d.Severity)
	assert.Contains(t, d.Suggestions, "orderId")
	assert.True(t, res.Valid)
}

func TestValidateSource_ExprSyntax(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: allocation-service.allocate
    body: { orderId: "${status ==}" }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeExprSyntax)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_UnknownStep(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: allocation-service.allocate
    body: { orderId: "${steps.nope.out.x}" }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeUnknownStep)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_StepOrder(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: allocation-service.allocate
    body: { orderId: "${steps.b.out.x}" }
  - id: b
    call: order-service.createOrder
    body: { customerId: c1, type: QCOM, pickup: {lat: 1, lng: 2}, drop: {lat: 1, lng: 2} }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeStepOrder)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_CrossStepBodyFieldCheck(t *testing.T) {
	src := `version: 1
steps:
  - id: create
    call: order-service.createOrder
    body: { customerId: c1, type: QCOM, pickup: {lat: 1, lng: 2}, drop: {lat: 1, lng: 2} }
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.body.bogusField}" }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeUnknownField)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_CrossStepBodyFieldCheck_Known(t *testing.T) {
	src := `version: 1
steps:
  - id: create
    call: order-service.createOrder
    body: { customerId: c1, type: QCOM, pickup: {lat: 1, lng: 2}, drop: {lat: 1, lng: 2} }
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.body.orderId}" }
`
	_, res := validateSrc(t, src)
	assert.Nil(t, diag(res, CodeUnknownField), "%+v", res.Diagnostics)
	assert.True(t, res.Valid)
}

func TestValidateSource_CurrentContextRootsAllowedInAssert(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: allocation-service.allocate
    body: { orderId: ord_1 }
    extract:
      allocId: out.nothingYet
    assert:
      - latency_ms < 5000
      - request.method == "POST"
`
	_, res := validateSrc(t, src)
	assert.Nil(t, diag(res, CodeContextRoot), "%+v", res.Diagnostics)
}

func TestValidateSource_BodyRefSkippedWhenOperationUnknown(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: order-service.bogusOp
    assert:
      - body.whatever == 1
`
	_, res := validateSrc(t, src)
	require.NotPanics(t, func() { _ = res.Valid })
	require.NotNil(t, diag(res, CodeUnknownOperation))
	assert.Nil(t, diag(res, CodeUnknownField), "%+v", res.Diagnostics)
}

func TestValidateSource_UnknownFlowInput(t *testing.T) {
	src := `version: 1
inputs:
  orderId: { type: string }
steps:
  - id: a
    call: allocation-service.allocate
    body: { orderId: "${inputs.bogus}" }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeUnknownFlowInput)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Contains(t, d.Suggestions, "orderId")
	assert.False(t, res.Valid)
}

func TestValidateSource_ContextRoot(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: rider-service.getRider
    input: { riderId: "${body.riderId}" }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeContextRoot)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_SecretContext(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: allocation-service.allocate
    body: { orderId: "${secret.TOKEN}" }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeSecretContext)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_SecretAllowedInHeaders(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: allocation-service.allocate
    body: { orderId: ord_1 }
    headers:
      Authorization: "Bearer ${secret.TOKEN}"
`
	_, res := validateSrc(t, src)
	assert.Nil(t, diag(res, CodeSecretContext), "%+v", res.Diagnostics)
	assert.True(t, res.Valid)
}

func TestValidateSource_UnknownField(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: rider-service.getRider
    input: { riderId: R1 }
    assert:
      - body.bogusField == 1
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeUnknownField)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Equal(t, domain.SeverityError, d.Severity)
	assert.False(t, res.Valid)
}

func TestValidateSource_UnknownField_DeeperIsWarning(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: rider-service.getRider
    input: { riderId: R1 }
    assert:
      - body.vehicle.bogus == 1
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeUnknownField)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Equal(t, domain.SeverityWarning, d.Severity)
	assert.True(t, res.Valid)
}

func TestValidateSource_UnknownField_ArrayNavigation(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: rider-service.searchRiders
    body: { lat: 1, lng: 2, radiusKm: 5 }
    assert:
      - body.riders[0].riderId == "r1"
`
	_, res := validateSrc(t, src)
	assert.Nil(t, diag(res, CodeUnknownField), "%+v", res.Diagnostics)
	assert.True(t, res.Valid)
}

func TestValidateSource_AssertionInvalid(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: rider-service.getRider
    input: { riderId: R1 }
    assert:
      - { status: 200, path: body.riderId }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeAssertionInvalid)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_DuplicateExtract(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: order-service.createOrder
    body: { customerId: c1, type: QCOM, pickup: {lat: 1, lng: 2}, drop: {lat: 1, lng: 2} }
    extract:
      orderId: body.orderId
      orderId: body.status
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeDuplicateExtract)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidate_InvalidDuration(t *testing.T) {
	// The schema restricts timeout/poll fields to a duration-shaped
	// pattern, so this is only reachable directly on a hand-built flow.
	v := NewValidator(newFakeCatalog())
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "a", Call: "order-service.createOrder", Timeout: "10x",
	}}}
	res := v.Validate(context.Background(), f)
	d := diag(res, CodeInvalidDuration)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidate_PollDurations(t *testing.T) {
	v := NewValidator(newFakeCatalog())
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "a", Call: "order-service.createOrder",
		Poll: &domain.Poll{Interval: "not-a-duration", Timeout: "30s"},
	}}}
	res := v.Validate(context.Background(), f)
	d := diag(res, CodeInvalidDuration)
	require.NotNil(t, d, "%+v", res.Diagnostics)
}

func TestValidate_UntilWithoutPollIsFine(t *testing.T) {
	v := NewValidator(newFakeCatalog())
	f := &domain.Flow{Version: 1, Steps: []domain.Step{{
		ID: "a", Call: "allocation-service.getAllocation",
		Input: map[string]any{"allocationId": "alloc_1"},
		Until: "status == 200",
	}}}
	res := v.Validate(context.Background(), f)
	assert.Nil(t, diag(res, CodeInvalidDuration))
	assert.True(t, res.Valid, "%+v", res.Diagnostics)
}

func TestValidateSource_NeverPanicsOnGarbage(t *testing.T) {
	v := NewValidator(newFakeCatalog())
	garbageInputs := []string{
		"",
		"\x00\x01\x02",
		"{{{{{",
		"version: 1\nsteps: not-an-array",
		"- just\n- a\n- list",
		"version: 1\nsteps:\n  - id: a\n    call: a.b\n    assert:\n      - \"${{{unterminated\"\n",
		strRepeat("a: ", 5000) + "1",
	}
	for _, src := range garbageInputs {
		require.NotPanics(t, func() {
			f, res := v.ValidateSource(context.Background(), src)
			require.NotNil(t, res)
			_ = f
		})
	}
}

func TestValidateSource_FixtureSmokeFlow(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "logistics", "allocation-service", "api", "flows", "smoke.flow.yaml"))
	require.NoError(t, err)
	_, res := validateSrc(t, string(data))
	for _, d := range res.Diagnostics {
		t.Logf("diagnostic: %+v", d)
	}
	assert.True(t, res.Valid, "smoke fixture flow should validate against the fake catalog: %+v", res.Diagnostics)
}

// ---- ${...} templates inside structured assertion values (eq/neq/contains/matches)

func TestValidateSource_AssertEqTemplate_StepOrder(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: allocation-service.allocate
    body: { orderId: ord_1 }
    assert:
      - { path: body.riderId, eq: "${steps.b.out.riderId}" }
  - id: b
    call: rider-service.getRider
    input: { riderId: R1 }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeStepOrder)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_AssertEqTemplate_UnknownField(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: order-service.createOrder
    body: { customerId: c1, type: QCOM, pickup: {lat: 1, lng: 2}, drop: {lat: 1, lng: 2} }
  - id: b
    call: rider-service.getRider
    input: { riderId: R1 }
    assert:
      - { path: body.riderId, eq: "${steps.a.body.bogusField}" }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeUnknownField)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_AssertEqTemplate_SecretContext(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: rider-service.getRider
    input: { riderId: R1 }
    assert:
      - { path: body.riderId, eq: "${secret.TOKEN}" }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeSecretContext)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_AssertMatchesTemplate_UnknownStep(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: rider-service.getRider
    input: { riderId: R1 }
    assert:
      - { path: body.riderId, matches: "${steps.nope.out.pattern}" }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeUnknownStep)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func strRepeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

func TestNearestSuggestions_ExportedWrapper(t *testing.T) {
	got := NearestSuggestions("rider_id", []string{"riderId", "orderId"}, 5)
	require.NotEmpty(t, got)
	assert.Equal(t, "riderId", got[0])
}

func TestNearestSuggestions(t *testing.T) {
	candidates := []string{"riderId", "orderId", "allocationId"}
	got := nearestSuggestions("rider_id", candidates, 5)
	require.NotEmpty(t, got)
	assert.Equal(t, "riderId", got[0])
}

func TestNearestSuggestions_NoOverlapFallsBackToAll(t *testing.T) {
	candidates := []string{"zzz", "aaa"}
	got := nearestSuggestions("qqq", candidates, 5)
	assert.Equal(t, []string{"aaa", "zzz"}, got)
}
