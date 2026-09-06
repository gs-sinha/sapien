package flow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// ---- parsing ---------------------------------------------------------------

func TestParse_SetupAndTeardown(t *testing.T) {
	src := `version: 1
id: with-cleanup
setup:
  - id: create
    call: order-service.createOrder
    body: { customerId: c1, type: QCOM, pickup: {lat: 1, lng: 2}, drop: {lat: 1, lng: 2} }
    extract:
      orderId: body.orderId
steps:
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.out.orderId}" }
    extract:
      allocationId: body.allocationId
teardown:
  - id: release
    call: allocation-service.releaseAllocation
    input: { allocationId: "${steps.allocate.out.allocationId}" }
`
	f, err := Parse(src)
	require.NoError(t, err)
	require.Len(t, f.Setup, 1)
	assert.Equal(t, "create", f.Setup[0].ID)
	require.Len(t, f.Steps, 1)
	assert.Equal(t, "allocate", f.Steps[0].ID)
	require.Len(t, f.Teardown, 1)
	assert.Equal(t, "release", f.Teardown[0].ID)
}

func TestParse_NoSetupOrTeardown_IsNil(t *testing.T) {
	f, err := Parse(baseFlowTemplate)
	require.NoError(t, err)
	assert.Nil(t, f.Setup)
	assert.Nil(t, f.Teardown)
}

func TestParse_SetupAcceptsExample(t *testing.T) {
	src := `version: 1
setup:
  - id: create
    example: create-qcom-order
steps:
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.out.orderId}" }
`
	f, err := Parse(src)
	require.NoError(t, err)
	require.Len(t, f.Setup, 1)
	assert.Equal(t, "create-qcom-order", f.Setup[0].Example)
}

// ---- materialize ------------------------------------------------------------

func TestMaterialize_SetupAndTeardownExamplesResolved(t *testing.T) {
	ex := createQcomExample()
	r := newFakeResolver(ex)
	f := &domain.Flow{
		Version: 1,
		Setup:   []domain.Step{{ID: "create", Example: "create-qcom-order"}},
		Steps:   []domain.Step{{ID: "a", Call: "allocation-service.allocate", Body: map[string]any{"orderId": "x"}}},
		Teardown: []domain.Step{
			{ID: "release", Call: "allocation-service.releaseAllocation", Input: map[string]any{"allocationId": "x"}},
		},
	}
	out, diags := Materialize(context.Background(), f, r)
	require.Empty(t, diags)
	require.Len(t, out.Setup, 1)
	assert.Equal(t, "order-service.createOrder", out.Setup[0].Call)
	require.Len(t, out.Teardown, 1)
	assert.Equal(t, "allocation-service.releaseAllocation", out.Teardown[0].Call)
}

func TestMaterialize_UnknownExampleInTeardown(t *testing.T) {
	f := &domain.Flow{
		Version:  1,
		Steps:    []domain.Step{{ID: "a", Call: "order-service.createOrder"}},
		Teardown: []domain.Step{{ID: "release", Example: "nope"}},
	}
	_, diags := Materialize(context.Background(), f, newFakeResolver())
	d := findCode(diags, CodeUnknownExample)
	require.NotNil(t, d, "%+v", diags)
	assert.Equal(t, "release", d.StepID)
}

// ---- validate: id uniqueness across all three lists -------------------------

func TestValidateSource_DuplicateID_AcrossSetupAndSteps(t *testing.T) {
	src := `version: 1
setup:
  - id: create
    call: order-service.createOrder
    body: { customerId: c1, type: QCOM, pickup: {lat: 1, lng: 2}, drop: {lat: 1, lng: 2} }
steps:
  - id: create
    call: allocation-service.allocate
    body: { orderId: ord_1 }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeStepIDDuplicate)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

func TestValidateSource_DuplicateID_AcrossStepsAndTeardown(t *testing.T) {
	src := `version: 1
steps:
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: ord_1 }
teardown:
  - id: allocate
    call: allocation-service.releaseAllocation
    input: { allocationId: alloc_1 }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeStepIDDuplicate)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.False(t, res.Valid)
}

// ---- validate: reference rules ---------------------------------------------

func TestValidateSource_MainStepReferencesAnySetupStep(t *testing.T) {
	src := `version: 1
setup:
  - id: create
    call: order-service.createOrder
    body: { customerId: c1, type: QCOM, pickup: {lat: 1, lng: 2}, drop: {lat: 1, lng: 2} }
    extract:
      orderId: body.orderId
steps:
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.out.orderId}" }
`
	_, res := validateSrc(t, src)
	assert.True(t, res.Valid, "%+v", res.Diagnostics)
}

func TestValidateSource_MainStepCannotReferenceTeardown(t *testing.T) {
	src := `version: 1
steps:
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: ord_1 }
    extract:
      allocationId: body.allocationId
  - id: check
    call: rider-service.getRider
    input: { riderId: "${steps.release.out.x}" }
teardown:
  - id: release
    call: allocation-service.releaseAllocation
    input: { allocationId: "${steps.allocate.out.allocationId}" }
`
	_, res := validateSrc(t, src)
	require.False(t, res.Valid)
	// "release" is a known step id that runs after "check", so this is a
	// STEP_ORDER violation, not an UNKNOWN_STEP one.
	d := diag(res, CodeStepOrder)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Equal(t, "check", d.StepID)
}

func TestValidateSource_TeardownReferencesMainAndSetup(t *testing.T) {
	src := `version: 1
setup:
  - id: create
    call: order-service.createOrder
    body: { customerId: c1, type: QCOM, pickup: {lat: 1, lng: 2}, drop: {lat: 1, lng: 2} }
    extract:
      orderId: body.orderId
steps:
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.out.orderId}" }
    extract:
      allocationId: body.allocationId
teardown:
  - id: release
    call: allocation-service.releaseAllocation
    input: { allocationId: "${steps.allocate.out.allocationId}" }
  - id: cancel
    call: rider-service.getRider
    input: { riderId: "${steps.create.out.orderId}" }
`
	_, res := validateSrc(t, src)
	assert.True(t, res.Valid, "%+v", res.Diagnostics)
}

func TestValidateSource_TeardownStepOrder(t *testing.T) {
	src := `version: 1
steps:
  - id: allocate
    call: allocation-service.allocate
    body: { orderId: ord_1 }
teardown:
  - id: a
    call: rider-service.getRider
    input: { riderId: "${steps.b.out.x}" }
  - id: b
    call: allocation-service.releaseAllocation
    input: { allocationId: "${steps.allocate.out.allocationId}" }
`
	_, res := validateSrc(t, src)
	require.False(t, res.Valid)
	d := diag(res, CodeStepOrder)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Equal(t, "a", d.StepID)
}

func TestValidateSource_SetupStepCannotReferenceLaterSetupStep(t *testing.T) {
	src := `version: 1
setup:
  - id: a
    call: order-service.createOrder
    body: { customerId: "${steps.b.out.x}", type: QCOM, pickup: {lat: 1, lng: 2}, drop: {lat: 1, lng: 2} }
  - id: b
    call: allocation-service.allocate
    body: { orderId: ord_1 }
steps:
  - id: c
    call: rider-service.getRider
    input: { riderId: R1 }
`
	_, res := validateSrc(t, src)
	require.False(t, res.Valid)
	d := diag(res, CodeStepOrder)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Equal(t, "a", d.StepID)
}

// ---- validate: duplicate extract inside setup/teardown ----------------------

func TestValidateSource_DuplicateExtract_InSetup(t *testing.T) {
	src := `version: 1
setup:
  - id: create
    call: order-service.createOrder
    body: { customerId: c1, type: QCOM, pickup: {lat: 1, lng: 2}, drop: {lat: 1, lng: 2} }
    extract:
      orderId: body.orderId
      orderId: body.status
steps:
  - id: a
    call: rider-service.getRider
    input: { riderId: R1 }
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeDuplicateExtract)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Equal(t, "create", d.StepID)
	assert.False(t, res.Valid)
}

func TestValidateSource_DuplicateExtract_InTeardown(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: rider-service.getRider
    input: { riderId: R1 }
teardown:
  - id: release
    call: allocation-service.releaseAllocation
    input: { allocationId: alloc_1 }
    extract:
      x: body.status
      x: body.orderId
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeDuplicateExtract)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Equal(t, "release", d.StepID)
	assert.False(t, res.Valid)
}

// ---- setup/teardown participate in the usual per-step checks ---------------

func TestValidateSource_UnknownOperation_InTeardown(t *testing.T) {
	src := `version: 1
steps:
  - id: a
    call: rider-service.getRider
    input: { riderId: R1 }
teardown:
  - id: release
    call: allocation-service.bogusOp
`
	_, res := validateSrc(t, src)
	d := diag(res, CodeUnknownOperation)
	require.NotNil(t, d, "%+v", res.Diagnostics)
	assert.Equal(t, "release", d.StepID)
	assert.False(t, res.Valid)
}

// ---- Uses() covers all three lists ------------------------------------------

func TestUses_IncludesSetupAndTeardown(t *testing.T) {
	f := &domain.Flow{
		Setup:    []domain.Step{{ID: "s", Call: "order-service.createOrder"}},
		Steps:    []domain.Step{{ID: "m", Call: "allocation-service.allocate"}},
		Teardown: []domain.Step{{ID: "t", Call: "allocation-service.releaseAllocation"}},
	}
	uses := Uses(f)
	assert.Equal(t, []string{
		"order-service.createOrder",
		"allocation-service.allocate",
		"allocation-service.releaseAllocation",
	}, uses)
}

// ---- Setup/teardown are documented -------------------------------------------

func TestReference_MentionsSetupAndTeardown(t *testing.T) {
	ref := Reference()
	assert.Contains(t, ref, "## Setup and teardown")
	assert.Contains(t, ref, "setup:")
	assert.Contains(t, ref, "teardown:")
}
