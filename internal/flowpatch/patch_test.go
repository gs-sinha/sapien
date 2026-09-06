package flowpatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const baseFlow = `version: 1
id: qcom-allocation
name: QCOM allocation
tags: [allocation, smoke]

steps:
  # create the order
  - id: create
    call: order-service.createOrder
    body:
      customerId: cust_123
    extract:
      orderId: body.orderId
    assert:
      - status == 201

  - id: allocate
    call: allocation-service.allocate
    body: { orderId: "${steps.create.out.orderId}" }
    assert:
      - status == 201
`

func TestApply_MergeStep_ReplacesOneFieldLeavesOthers(t *testing.T) {
	out, err := Apply(baseFlow, []Op{{
		Kind:   KindMergeStep,
		ID:     "create",
		Fields: map[string]any{"assert": []any{"status == 200", "response.body.orderId != ''"}},
	}})
	require.NoError(t, err)

	assert.Contains(t, out, "# create the order", "untouched comment must survive")
	assert.Contains(t, out, "customerId: cust_123", "untouched body must survive")
	assert.Contains(t, out, "status == 200")
	assert.Contains(t, out, "response.body.orderId != ''")

	// The result must itself be valid YAML with the expected shape.
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	require.Len(t, steps, 2)
	created := steps[0].(map[string]any)
	assert.Equal(t, "order-service.createOrder", created["call"])
	assert.Equal(t, []any{"status == 200", "response.body.orderId != ''"}, created["assert"],
		"the old assert value for `create` must be replaced, not merged")
	allocate := steps[1].(map[string]any)
	assert.Equal(t, []any{"status == 201"}, allocate["assert"], "the untouched `allocate` step's assert must survive")
}

func TestApply_MergeStep_UnknownField(t *testing.T) {
	_, err := Apply(baseFlow, []Op{{
		Kind:   KindMergeStep,
		ID:     "create",
		Fields: map[string]any{"id": "nope"},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown field "id"`)
}

func TestApply_MergeStep_UnknownID(t *testing.T) {
	_, err := Apply(baseFlow, []Op{{
		Kind:   KindMergeStep,
		ID:     "does-not-exist",
		Fields: map[string]any{"until": "status == 200"},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown step id "does-not-exist"`)
	assert.Contains(t, err.Error(), "create")
	assert.Contains(t, err.Error(), "allocate")
}

func TestApply_SetStep_ReplacesWholeStep(t *testing.T) {
	out, err := Apply(baseFlow, []Op{{
		Kind: KindSetStep,
		ID:   "allocate",
		Step: map[string]any{
			"id":   "allocate",
			"call": "allocation-service.allocate",
			"body": map[string]any{"orderId": "${steps.create.out.orderId}", "priority": "high"},
		},
	}})
	require.NoError(t, err)
	assert.Contains(t, out, "priority: high")
	assert.NotContains(t, out, "status == 201\n  \n") // old assert block removed (loosely)

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	allocate := steps[1].(map[string]any)
	assert.Nil(t, allocate["assert"], "assert was not in the replacement, so it must be gone")
}

func TestApply_SetStep_IDMismatch(t *testing.T) {
	_, err := Apply(baseFlow, []Op{{
		Kind: KindSetStep,
		ID:   "allocate",
		Step: map[string]any{"id": "renamed", "call": "allocation-service.allocate"},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `does not match`)
}

func TestApply_SetStep_DefaultsMissingID(t *testing.T) {
	out, err := Apply(baseFlow, []Op{{
		Kind: KindSetStep,
		ID:   "allocate",
		Step: map[string]any{"call": "allocation-service.allocate", "body": map[string]any{"orderId": "x"}},
	}})
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	allocate := steps[1].(map[string]any)
	assert.Equal(t, "allocate", allocate["id"])
}

func TestApply_AddStep_AppendsByDefault(t *testing.T) {
	out, err := Apply(baseFlow, []Op{{
		Kind: KindAddStep,
		Step: map[string]any{"id": "verify", "call": "rider-service.getRider"},
	}})
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	require.Len(t, steps, 3)
	assert.Equal(t, "verify", steps[2].(map[string]any)["id"])
}

func TestApply_AddStep_After(t *testing.T) {
	out, err := Apply(baseFlow, []Op{{
		Kind:  KindAddStep,
		After: "create",
		Step:  map[string]any{"id": "middle", "call": "x.y"},
	}})
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	require.Len(t, steps, 3)
	assert.Equal(t, "create", steps[0].(map[string]any)["id"])
	assert.Equal(t, "middle", steps[1].(map[string]any)["id"])
	assert.Equal(t, "allocate", steps[2].(map[string]any)["id"])
}

func TestApply_AddStep_Before(t *testing.T) {
	out, err := Apply(baseFlow, []Op{{
		Kind:   KindAddStep,
		Before: "allocate",
		Step:   map[string]any{"id": "middle", "call": "x.y"},
	}})
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	require.Len(t, steps, 3)
	assert.Equal(t, "middle", steps[1].(map[string]any)["id"])
}

func TestApply_AddStep_ToNewSetupPhase(t *testing.T) {
	out, err := Apply(baseFlow, []Op{{
		Kind:  KindAddStep,
		Phase: "setup",
		Step:  map[string]any{"id": "provision", "call": "bag-service.create"},
	}})
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	setup, ok := doc["setup"].([]any)
	require.True(t, ok, "expected a new top-level setup: list, got %+v", doc)
	require.Len(t, setup, 1)
	assert.Equal(t, "provision", setup[0].(map[string]any)["id"])

	// setup: must land before steps: in the document text.
	setupIdx := indexOfSubstring(out, "setup:")
	stepsIdx := indexOfSubstring(out, "steps:")
	require.NotEqual(t, -1, setupIdx)
	require.NotEqual(t, -1, stepsIdx)
	assert.Less(t, setupIdx, stepsIdx)
}

func TestApply_AddStep_ToNewTeardownPhase(t *testing.T) {
	out, err := Apply(baseFlow, []Op{{
		Kind:  KindAddStep,
		Phase: "teardown",
		Step:  map[string]any{"id": "cleanup", "call": "bag-service.delete"},
	}})
	require.NoError(t, err)

	stepsIdx := indexOfSubstring(out, "steps:")
	teardownIdx := indexOfSubstring(out, "teardown:")
	require.NotEqual(t, -1, teardownIdx)
	assert.Less(t, stepsIdx, teardownIdx)
}

func TestApply_AddStep_DuplicateID(t *testing.T) {
	_, err := Apply(baseFlow, []Op{{
		Kind: KindAddStep,
		Step: map[string]any{"id": "create", "call": "x.y"},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestApply_AddStep_MissingID(t *testing.T) {
	_, err := Apply(baseFlow, []Op{{
		Kind: KindAddStep,
		Step: map[string]any{"call": "x.y"},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must include an id")
}

func TestApply_AddStep_AfterAndBeforeBothSet(t *testing.T) {
	_, err := Apply(baseFlow, []Op{{
		Kind:   KindAddStep,
		After:  "create",
		Before: "allocate",
		Step:   map[string]any{"id": "middle", "call": "x.y"},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "after or before, not both")
}

func TestApply_AddStep_AfterNotFound(t *testing.T) {
	_, err := Apply(baseFlow, []Op{{
		Kind:  KindAddStep,
		After: "does-not-exist",
		Step:  map[string]any{"id": "middle", "call": "x.y"},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in phase")
}

func TestApply_AddStep_UnknownPhase(t *testing.T) {
	_, err := Apply(baseFlow, []Op{{
		Kind:  KindAddStep,
		Phase: "bogus",
		Step:  map[string]any{"id": "x", "call": "x.y"},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown phase")
}

func TestApply_RemoveStep(t *testing.T) {
	out, err := Apply(baseFlow, []Op{{Kind: KindRemoveStep, ID: "allocate"}})
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	require.Len(t, steps, 1)
	assert.Equal(t, "create", steps[0].(map[string]any)["id"])
}

func TestApply_RemoveStep_UnknownID(t *testing.T) {
	_, err := Apply(baseFlow, []Op{{Kind: KindRemoveStep, ID: "nope"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown step id "nope"`)
}

func TestApply_SetInputs(t *testing.T) {
	out, err := Apply(baseFlow, []Op{{
		Kind:   KindSetInputs,
		Inputs: map[string]any{"customerId": map[string]any{"type": "string", "default": "cust_1"}},
	}})
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	inputs, ok := doc["inputs"].(map[string]any)
	require.True(t, ok)
	customerID := inputs["customerId"].(map[string]any)
	assert.Equal(t, "string", customerID["type"])

	// Replacing again must overwrite, not merge.
	out2, err := Apply(out, []Op{{
		Kind:   KindSetInputs,
		Inputs: map[string]any{"city": map[string]any{"type": "string"}},
	}})
	require.NoError(t, err)
	var doc2 map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out2), &doc2))
	inputs2 := doc2["inputs"].(map[string]any)
	assert.Len(t, inputs2, 1)
	assert.Contains(t, inputs2, "city")
}

func TestApply_SetMeta(t *testing.T) {
	name := "Renamed flow"
	desc := "A new description"
	tags := []string{"a", "b"}
	out, err := Apply(baseFlow, []Op{{
		Kind: KindSetMeta,
		Meta: &Meta{Name: &name, Description: &desc, Tags: &tags},
	}})
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	assert.Equal(t, "Renamed flow", doc["name"])
	assert.Equal(t, "A new description", doc["description"])
	gotTags := doc["tags"].([]any)
	assert.Equal(t, []any{"a", "b"}, gotTags)
}

func TestApply_SetMeta_PartialLeavesOthersAlone(t *testing.T) {
	name := "Only rename"
	out, err := Apply(baseFlow, []Op{{Kind: KindSetMeta, Meta: &Meta{Name: &name}}})
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	assert.Equal(t, "Only rename", doc["name"])
	tags := doc["tags"].([]any)
	assert.Equal(t, []any{"allocation", "smoke"}, tags, "tags must be untouched")
}

func TestApply_SetMeta_NilMeta(t *testing.T) {
	_, err := Apply(baseFlow, []Op{{Kind: KindSetMeta}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires meta")
}

func TestApply_MultipleOps_AppliedInOrder(t *testing.T) {
	out, err := Apply(baseFlow, []Op{
		{Kind: KindAddStep, Step: map[string]any{"id": "verify", "call": "rider-service.getRider"}},
		{Kind: KindMergeStep, ID: "verify", Fields: map[string]any{"until": "status == 200"}},
		{Kind: KindRemoveStep, ID: "create"},
	})
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	require.Len(t, steps, 2)
	assert.Equal(t, "allocate", steps[0].(map[string]any)["id"])
	verify := steps[1].(map[string]any)
	assert.Equal(t, "verify", verify["id"])
	assert.Equal(t, "status == 200", verify["until"])
}

func TestApply_UnknownOpKind(t *testing.T) {
	_, err := Apply(baseFlow, []Op{{Kind: "bogus"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown op kind")
	assert.Contains(t, err.Error(), "op 0 (bogus)")
}

func TestApply_EmptyOpKind(t *testing.T) {
	_, err := Apply(baseFlow, []Op{{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no kind")
}

func TestApply_NoOps_ReturnsSourceUnchanged(t *testing.T) {
	out, err := Apply(baseFlow, nil)
	require.NoError(t, err)
	var got, want map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &got))
	require.NoError(t, yaml.Unmarshal([]byte(baseFlow), &want))
	assert.Equal(t, want, got)
}

func TestApply_MalformedYAML(t *testing.T) {
	_, err := Apply("not: valid: yaml: [", nil)
	require.Error(t, err)
}

func TestApply_NonMappingDocument(t *testing.T) {
	_, err := Apply("- 1\n- 2\n", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a YAML mapping")
}

// TestApply_BareAndStructuredAssertionsRoundTrip covers the exact complaint
// in docs/feedback/2026-09-05-41-step-flow-session.md item 2: a structured
// assertion using `expr:` (the form get_flow renders a bare-string
// assertion as) must survive a merge_step untouched.
func TestApply_BareAndStructuredAssertionsRoundTrip(t *testing.T) {
	out, err := Apply(baseFlow, []Op{{
		Kind: KindMergeStep,
		ID:   "create",
		Fields: map[string]any{
			"assert": []any{
				map[string]any{"expr": "response.status == 201"},
				map[string]any{"path": "body.orderId", "exists": true},
			},
		},
	}})
	require.NoError(t, err)
	assert.Contains(t, out, "expr: response.status == 201")

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	created := steps[0].(map[string]any)
	assertList := created["assert"].([]any)
	require.Len(t, assertList, 2)
	first := assertList[0].(map[string]any)
	assert.Equal(t, "response.status == 201", first["expr"])
}

// TestApply_NumberFieldsRenderPlainly guards against a JSON-decoded number
// (float64, since op.Fields comes from encoding/json) rendering with an
// unwanted decimal point or exponent, e.g. `status: 2.01e+02`.
func TestApply_NumberFieldsRenderPlainly(t *testing.T) {
	out, err := Apply(baseFlow, []Op{{
		Kind: KindMergeStep,
		ID:   "create",
		Fields: map[string]any{
			"assert": []any{map[string]any{"status": float64(200)}},
		},
	}})
	require.NoError(t, err)
	assert.Contains(t, out, "status: 200")
	assert.NotContains(t, out, "200.0")
	assert.NotContains(t, out, "2e+02")
}

func TestApply_CommentsAndKeyOrderSurviveUntouchedSteps(t *testing.T) {
	out, err := Apply(baseFlow, []Op{{Kind: KindMergeStep, ID: "allocate", Fields: map[string]any{"until": "status == 201"}}})
	require.NoError(t, err)
	assert.Contains(t, out, "# create the order", "the comment above the untouched `create` step must survive")
	// key order for the untouched `create` step: call, body, extract, assert.
	callIdx := indexOfSubstring(out, "call: order-service.createOrder")
	bodyIdx := indexOfSubstring(out, "body:")
	extractIdx := indexOfSubstring(out, "extract:")
	require.True(t, callIdx < bodyIdx && bodyIdx < extractIdx, "key order must be preserved: %s", out)
}

func indexOfSubstring(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
