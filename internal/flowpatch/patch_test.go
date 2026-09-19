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
	res, err := Apply(baseFlow, []Op{{
		Kind:   KindMergeStep,
		ID:     "create",
		Fields: map[string]any{"assert": []any{"status == 200", "response.body.orderId != ''"}},
	}})
	require.NoError(t, err)
	out := res.YAML

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

// TestApply_MergeStep_When confirms `when` (PLAN §34f.7) is on the
// merge_step whitelist, alongside the other bare-CEL step fields.
func TestApply_MergeStep_When(t *testing.T) {
	res, err := Apply(baseFlow, []Op{{
		Kind:   KindMergeStep,
		ID:     "allocate",
		Fields: map[string]any{"when": "inputs.releaseNow"},
	}})
	require.NoError(t, err)
	out := res.YAML

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	allocate := steps[1].(map[string]any)
	assert.Equal(t, "inputs.releaseNow", allocate["when"])
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
	res, err := Apply(baseFlow, []Op{{
		Kind: KindSetStep,
		ID:   "allocate",
		Step: map[string]any{
			"id":   "allocate",
			"call": "allocation-service.allocate",
			"body": map[string]any{"orderId": "${steps.create.out.orderId}", "priority": "high"},
		},
	}})
	require.NoError(t, err)
	out := res.YAML
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
	res, err := Apply(baseFlow, []Op{{
		Kind: KindSetStep,
		ID:   "allocate",
		Step: map[string]any{"call": "allocation-service.allocate", "body": map[string]any{"orderId": "x"}},
	}})
	require.NoError(t, err)
	out := res.YAML
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	allocate := steps[1].(map[string]any)
	assert.Equal(t, "allocate", allocate["id"])
}

func TestApply_AddStep_AppendsByDefault(t *testing.T) {
	res, err := Apply(baseFlow, []Op{{
		Kind: KindAddStep,
		Step: map[string]any{"id": "verify", "call": "rider-service.getRider"},
	}})
	require.NoError(t, err)
	out := res.YAML
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	require.Len(t, steps, 3)
	assert.Equal(t, "verify", steps[2].(map[string]any)["id"])
}

func TestApply_AddStep_After(t *testing.T) {
	res, err := Apply(baseFlow, []Op{{
		Kind:  KindAddStep,
		After: "create",
		Step:  map[string]any{"id": "middle", "call": "x.y"},
	}})
	require.NoError(t, err)
	out := res.YAML
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	require.Len(t, steps, 3)
	assert.Equal(t, "create", steps[0].(map[string]any)["id"])
	assert.Equal(t, "middle", steps[1].(map[string]any)["id"])
	assert.Equal(t, "allocate", steps[2].(map[string]any)["id"])
}

func TestApply_AddStep_Before(t *testing.T) {
	res, err := Apply(baseFlow, []Op{{
		Kind:   KindAddStep,
		Before: "allocate",
		Step:   map[string]any{"id": "middle", "call": "x.y"},
	}})
	require.NoError(t, err)
	out := res.YAML
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	require.Len(t, steps, 3)
	assert.Equal(t, "middle", steps[1].(map[string]any)["id"])
}

func TestApply_AddStep_ToNewSetupPhase(t *testing.T) {
	res, err := Apply(baseFlow, []Op{{
		Kind:  KindAddStep,
		Phase: "setup",
		Step:  map[string]any{"id": "provision", "call": "bag-service.create"},
	}})
	require.NoError(t, err)
	out := res.YAML
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
	res, err := Apply(baseFlow, []Op{{
		Kind:  KindAddStep,
		Phase: "teardown",
		Step:  map[string]any{"id": "cleanup", "call": "bag-service.delete"},
	}})
	require.NoError(t, err)
	out := res.YAML

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

// TestApply_AddStep_AfterNotFound also guards HALF1's fix: the anchor is
// now looked up flow-wide (golden text updated from the old "not found in
// phase %q", which only searched the target phase, to "not found anywhere
// in the flow" -- see TestApply_AddStep_BeforeAnchorInAnotherPhase for the
// bug this replaces: an anchor that WAS present just in a different phase).
func TestApply_AddStep_AfterNotFound(t *testing.T) {
	_, err := Apply(baseFlow, []Op{{
		Kind:  KindAddStep,
		After: "does-not-exist",
		Step:  map[string]any{"id": "middle", "call": "x.y"},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found anywhere in the flow")
	assert.Contains(t, err.Error(), `create (phase "steps")`, "ids present must be labeled with their location")
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
	res, err := Apply(baseFlow, []Op{{Kind: KindRemoveStep, ID: "allocate"}})
	require.NoError(t, err)
	out := res.YAML
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
	res, err := Apply(baseFlow, []Op{{
		Kind:   KindSetInputs,
		Inputs: map[string]any{"customerId": map[string]any{"type": "string", "default": "cust_1"}},
	}})
	require.NoError(t, err)
	out := res.YAML
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	inputs, ok := doc["inputs"].(map[string]any)
	require.True(t, ok)
	customerID := inputs["customerId"].(map[string]any)
	assert.Equal(t, "string", customerID["type"])

	// Replacing again must overwrite, not merge.
	res, err = Apply(out, []Op{{
		Kind:   KindSetInputs,
		Inputs: map[string]any{"city": map[string]any{"type": "string"}},
	}})
	require.NoError(t, err)
	out2 := res.YAML
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
	res, err := Apply(baseFlow, []Op{{
		Kind: KindSetMeta,
		Meta: &Meta{Name: &name, Description: &desc, Tags: &tags},
	}})
	require.NoError(t, err)
	out := res.YAML
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	assert.Equal(t, "Renamed flow", doc["name"])
	assert.Equal(t, "A new description", doc["description"])
	gotTags := doc["tags"].([]any)
	assert.Equal(t, []any{"a", "b"}, gotTags)
}

func TestApply_SetMeta_PartialLeavesOthersAlone(t *testing.T) {
	name := "Only rename"
	res, err := Apply(baseFlow, []Op{{Kind: KindSetMeta, Meta: &Meta{Name: &name}}})
	require.NoError(t, err)
	out := res.YAML
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
	res, err := Apply(baseFlow, []Op{
		{Kind: KindAddStep, Step: map[string]any{"id": "verify", "call": "rider-service.getRider"}},
		{Kind: KindMergeStep, ID: "verify", Fields: map[string]any{"until": "status == 200"}},
		{Kind: KindRemoveStep, ID: "create"},
	})
	require.NoError(t, err)
	out := res.YAML
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
	res, err := Apply(baseFlow, nil)
	require.NoError(t, err)
	out := res.YAML
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
	res, err := Apply(baseFlow, []Op{{
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
	out := res.YAML
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
	res, err := Apply(baseFlow, []Op{{
		Kind: KindMergeStep,
		ID:   "create",
		Fields: map[string]any{
			"assert": []any{map[string]any{"status": float64(200)}},
		},
	}})
	require.NoError(t, err)
	out := res.YAML
	assert.Contains(t, out, "status: 200")
	assert.NotContains(t, out, "200.0")
	assert.NotContains(t, out, "2e+02")
}

func TestApply_CommentsAndKeyOrderSurviveUntouchedSteps(t *testing.T) {
	res, err := Apply(baseFlow, []Op{{Kind: KindMergeStep, ID: "allocate", Fields: map[string]any{"until": "status == 201"}}})
	require.NoError(t, err)
	out := res.YAML
	assert.Contains(t, out, "# create the order", "the comment above the untouched `create` step must survive")
	// key order for the untouched `create` step: call, body, extract, assert.
	callIdx := indexOfSubstring(out, "call: order-service.createOrder")
	bodyIdx := indexOfSubstring(out, "body:")
	extractIdx := indexOfSubstring(out, "extract:")
	require.True(t, callIdx < bodyIdx && bodyIdx < extractIdx, "key order must be preserved: %s", out)
}

func indexOfSubstring(s, substr string) int {
	return indexOfSubstringFrom(s, substr, 0)
}

// indexOfSubstringFrom is indexOfSubstring, searching only from start
// onward -- for asserting relative key order in a document where a key
// name (e.g. "input:", "body:") legitimately appears more than once, on
// different steps.
func indexOfSubstringFrom(s, substr string, start int) int {
	if start < 0 {
		start = 0
	}
	for i := start; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// ---- loop blocks (PLAN §34f.8) --------------------------------------------

const blockFlow = `version: 1
id: bulk-allocate
steps:
  - id: create
    call: order-service.createOrder
    body: { customerId: cust_123 }
    extract:
      orderId: body.orderId

  - id: each
    foreach: "['a', 'b']"
    max: 10
    steps:
      - id: allocate
        call: allocation-service.allocate
        body: { orderId: "${iter.item}" }
        assert:
          - status == 201

  - id: verify
    call: allocation-service.getAllocation
    input: { allocationId: x1 }
`

func TestApply_SetStep_NestedStepByID(t *testing.T) {
	res, err := Apply(blockFlow, []Op{{
		Kind: KindSetStep,
		ID:   "allocate",
		Step: map[string]any{
			"id":   "allocate",
			"call": "allocation-service.allocate",
			"body": map[string]any{"orderId": "${iter.item}", "priority": "high"},
		},
	}})
	require.NoError(t, err)
	out := res.YAML

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	block := steps[1].(map[string]any)
	nested := block["steps"].([]any)
	allocate := nested[0].(map[string]any)
	body := allocate["body"].(map[string]any)
	assert.Equal(t, "high", body["priority"], "set_step must find and replace the nested step in place")
}

func TestApply_MergeStep_NestedStepByID(t *testing.T) {
	res, err := Apply(blockFlow, []Op{{
		Kind:   KindMergeStep,
		ID:     "allocate",
		Fields: map[string]any{"when": "iter.index == 0"},
	}})
	require.NoError(t, err)
	out := res.YAML

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	block := steps[1].(map[string]any)
	nested := block["steps"].([]any)
	allocate := nested[0].(map[string]any)
	assert.Equal(t, "iter.index == 0", allocate["when"])
}

func TestApply_RemoveStep_NestedStepByID(t *testing.T) {
	res, err := Apply(blockFlow, []Op{{Kind: KindRemoveStep, ID: "allocate"}})
	require.NoError(t, err)
	out := res.YAML

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	block := steps[1].(map[string]any)
	nested, _ := block["steps"].([]any)
	assert.Empty(t, nested)
}

// TestApply_RemoveStep_BlockRemovesChildren confirms removing a loop
// block's own id removes its nested steps too -- they are part of the same
// YAML node, so no special-casing is needed beyond finding the block.
func TestApply_RemoveStep_BlockRemovesChildren(t *testing.T) {
	res, err := Apply(blockFlow, []Op{{Kind: KindRemoveStep, ID: "each"}})
	require.NoError(t, err)
	out := res.YAML
	assert.NotContains(t, out, "id: allocate")
	assert.NotContains(t, out, "foreach:")

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	require.Len(t, steps, 2, "create and verify remain; each and its nested allocate are gone")
}

func TestApply_AddStep_Into(t *testing.T) {
	res, err := Apply(blockFlow, []Op{{
		Kind: KindAddStep,
		Into: "each",
		Step: map[string]any{
			"id":   "log",
			"call": "allocation-service.getAllocation",
			"input": map[string]any{
				"allocationId": "${iter.item}",
			},
		},
	}})
	require.NoError(t, err)
	out := res.YAML

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	block := steps[1].(map[string]any)
	nested := block["steps"].([]any)
	require.Len(t, nested, 2)
	assert.Equal(t, "allocate", nested[0].(map[string]any)["id"])
	assert.Equal(t, "log", nested[1].(map[string]any)["id"])
}

func TestApply_AddStep_IntoWithAfter(t *testing.T) {
	// Add a second nested step first, then insert a third `after: allocate`
	// (a sibling inside the block), confirming After is scoped to the
	// block's own list, not the top-level `steps:`.
	res, err := Apply(blockFlow, []Op{
		{Kind: KindAddStep, Into: "each", Step: map[string]any{"id": "b", "call": "allocation-service.getAllocation", "input": map[string]any{"allocationId": "x"}}},
		{Kind: KindAddStep, Into: "each", After: "allocate", Step: map[string]any{"id": "mid", "call": "allocation-service.getAllocation", "input": map[string]any{"allocationId": "y"}}},
	})
	require.NoError(t, err)
	out := res.YAML

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	block := steps[1].(map[string]any)
	nested := block["steps"].([]any)
	require.Len(t, nested, 3)
	ids := []string{
		nested[0].(map[string]any)["id"].(string),
		nested[1].(map[string]any)["id"].(string),
		nested[2].(map[string]any)["id"].(string),
	}
	assert.Equal(t, []string{"allocate", "mid", "b"}, ids)
}

func TestApply_AddStep_IntoUnknownBlock(t *testing.T) {
	_, err := Apply(blockFlow, []Op{{
		Kind: KindAddStep,
		Into: "nope",
		Step: map[string]any{"id": "x", "call": "allocation-service.getAllocation", "input": map[string]any{"allocationId": "1"}},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `into block "nope" not found`)
}

// TestApply_MergeStep_LoopFields confirms foreach/repeat/max/break_when/
// on_error are all on the merge_step whitelist (PLAN §34f.8), and that
// `steps` (a block's nested list) deliberately is not.
func TestApply_MergeStep_LoopFields(t *testing.T) {
	res, err := Apply(blockFlow, []Op{{
		Kind:   KindMergeStep,
		ID:     "each",
		Fields: map[string]any{"max": float64(50), "break_when": "iter.index >= 1", "on_error": "continue"},
	}})
	require.NoError(t, err)
	out := res.YAML

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	steps := doc["steps"].([]any)
	block := steps[1].(map[string]any)
	assert.EqualValues(t, 50, block["max"])
	assert.Equal(t, "iter.index >= 1", block["break_when"])
	assert.Equal(t, "continue", block["on_error"])
}

func TestApply_MergeStep_StepsFieldRejected(t *testing.T) {
	_, err := Apply(blockFlow, []Op{{
		Kind:   KindMergeStep,
		ID:     "each",
		Fields: map[string]any{"steps": []any{}},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown field "steps"`)
}

// ---- HALF 1 (discussion #4): add_step's before/after anchor is resolved
// flow-wide, and the new step's phase/block is inferred from the anchor ---

// TestApply_AddStep_BeforeAnchorInAnotherPhase_Discussion4 is the exact
// report: `before: "td"` names a teardown step while add_step's default
// phase is "steps" -- before the fix this answered `before step "td" not
// found in phase "steps"; ids present: b, td`, i.e. it listed td as present
// and still refused, because the anchor was looked up only inside the
// (default) target phase instead of flow-wide.
func TestApply_AddStep_BeforeAnchorInAnotherPhase_Discussion4(t *testing.T) {
	const flow = `version: 1
id: demo
steps:
  - id: b
    call: x.y
teardown:
  - id: td
    call: x.y
`
	res, err := Apply(flow, []Op{{
		Kind:   KindAddStep,
		Before: "td",
		Step:   map[string]any{"id": "cleanup", "call": "x.y"},
	}})
	require.NoError(t, err, "before naming a step in another phase must infer that phase, not refuse it")
	out := res.YAML

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc))
	teardown := doc["teardown"].([]any)
	require.Len(t, teardown, 2)
	assert.Equal(t, "cleanup", teardown[0].(map[string]any)["id"], "inserted before td, inside teardown -- not appended to steps")
	assert.Equal(t, "td", teardown[1].(map[string]any)["id"])
	steps := doc["steps"].([]any)
	require.Len(t, steps, 1, "the main steps: list must be untouched")
}

// comboFlow gives every add_step anchor test one step in each location: a
// top-level setup step, a top-level main step, a step nested inside a loop
// block (itself in steps), and a top-level teardown step.
const comboFlow = `version: 1
id: combo
setup:
  - id: su
    call: x.y
steps:
  - id: a
    call: x.y
  - id: each
    foreach: "['a']"
    steps:
      - id: nested
        call: x.y
teardown:
  - id: td
    call: x.y
`

// addStepAnchorCase is one cell of the anchor x phase x into table: anchor
// is always used as add_step's `before`. wantErrSub == "" means the op must
// succeed, inserting the new step into wantHolder (and, when wantBlock !=
// "", inside that block's own nested steps); otherwise it's a substring the
// resulting error must contain.
type addStepAnchorCase struct {
	name       string
	anchor     string
	phase      string
	into       string
	wantErrSub string
	wantHolder string
	wantBlock  string
}

// TestApply_AddStep_AnchorPhaseIntoCombinations is HALF1's table test: for
// an anchor in each of setup/steps/a nested block/teardown, every
// combination of phase (absent/matching/contradicting) and into
// (absent/matching/contradicting) that the current Op API can express.
// Phase's zero value ("") is indistinguishable from "not provided" (see
// phaseKeyFor), so "absent" and "explicitly the anchor's own phase, via
// the empty string" are the same case; "steps" is accepted as the main
// list's explicit name precisely so a "matching" case is expressible for
// an anchor that isn't nested. Into's zero value is unambiguously "not
// provided" (a block id is never empty).
func TestApply_AddStep_AnchorPhaseIntoCombinations(t *testing.T) {
	cases := []addStepAnchorCase{
		// anchor "su": phase setup, block "" (top-level)
		{name: "su/phase-absent/into-absent", anchor: "su", phase: "", into: "", wantHolder: "setup"},
		{name: "su/phase-matching/into-absent", anchor: "su", phase: "setup", into: "", wantHolder: "setup"},
		{name: "su/phase-contradicting-steps/into-absent", anchor: "su", phase: "steps", into: "", wantErrSub: `is in phase "setup"; phase "steps" does not match`},
		{name: "su/phase-contradicting-teardown/into-absent", anchor: "su", phase: "teardown", into: "", wantErrSub: `is in phase "setup"; phase "teardown" does not match`},
		{name: "su/phase-absent/into-contradicting", anchor: "su", phase: "", into: "each", wantErrSub: `is in phase "setup"; into "each" does not match`},
		{name: "su/phase-matching/into-contradicting", anchor: "su", phase: "setup", into: "each", wantErrSub: `is in phase "setup"; into "each" does not match`},

		// anchor "a": phase steps, block "" (top-level)
		{name: "a/phase-absent/into-absent", anchor: "a", phase: "", into: "", wantHolder: "steps"},
		{name: "a/phase-matching-steps/into-absent", anchor: "a", phase: "steps", into: "", wantHolder: "steps"},
		{name: "a/phase-contradicting-setup/into-absent", anchor: "a", phase: "setup", into: "", wantErrSub: `is in phase "steps"; phase "setup" does not match`},
		{name: "a/phase-contradicting-teardown/into-absent", anchor: "a", phase: "teardown", into: "", wantErrSub: `is in phase "steps"; phase "teardown" does not match`},
		{name: "a/phase-absent/into-contradicting", anchor: "a", phase: "", into: "each", wantErrSub: `is in phase "steps"; into "each" does not match`},
		{name: "a/phase-matching/into-contradicting", anchor: "a", phase: "steps", into: "each", wantErrSub: `is in phase "steps"; into "each" does not match`},

		// anchor "nested": phase steps, block "each"
		{name: "nested/phase-absent/into-absent", anchor: "nested", phase: "", into: "", wantHolder: "steps", wantBlock: "each"},
		{name: "nested/phase-matching/into-absent", anchor: "nested", phase: "steps", into: "", wantHolder: "steps", wantBlock: "each"},
		{name: "nested/phase-contradicting-setup/into-absent", anchor: "nested", phase: "setup", into: "", wantErrSub: `is in block "each" (phase "steps"); phase "setup" does not match`},
		{name: "nested/phase-contradicting-teardown/into-absent", anchor: "nested", phase: "teardown", into: "", wantErrSub: `is in block "each" (phase "steps"); phase "teardown" does not match`},
		{name: "nested/phase-absent/into-matching", anchor: "nested", phase: "", into: "each", wantHolder: "steps", wantBlock: "each"},
		{name: "nested/phase-matching/into-matching", anchor: "nested", phase: "steps", into: "each", wantHolder: "steps", wantBlock: "each"},
		{name: "nested/phase-contradicting/into-matching", anchor: "nested", phase: "setup", into: "each", wantErrSub: `is in block "each" (phase "steps"); phase "setup" does not match`},
		{name: "nested/phase-absent/into-contradicting", anchor: "nested", phase: "", into: "other", wantErrSub: `is in block "each" (phase "steps"); into "other" does not match`},
		{name: "nested/phase-matching/into-contradicting", anchor: "nested", phase: "steps", into: "other", wantErrSub: `is in block "each" (phase "steps"); into "other" does not match`},
		{name: "nested/phase-contradicting/into-contradicting", anchor: "nested", phase: "setup", into: "other", wantErrSub: `is in block "each" (phase "steps"); phase "setup" does not match`},

		// anchor "td": phase teardown, block "" (top-level)
		{name: "td/phase-absent/into-absent", anchor: "td", phase: "", into: "", wantHolder: "teardown"},
		{name: "td/phase-matching/into-absent", anchor: "td", phase: "teardown", into: "", wantHolder: "teardown"},
		{name: "td/phase-contradicting-setup/into-absent", anchor: "td", phase: "setup", into: "", wantErrSub: `is in phase "teardown"; phase "setup" does not match`},
		{name: "td/phase-contradicting-steps/into-absent", anchor: "td", phase: "steps", into: "", wantErrSub: `is in phase "teardown"; phase "steps" does not match`},
		{name: "td/phase-absent/into-contradicting", anchor: "td", phase: "", into: "each", wantErrSub: `is in phase "teardown"; into "each" does not match`},
		{name: "td/phase-matching/into-contradicting", anchor: "td", phase: "teardown", into: "each", wantErrSub: `is in phase "teardown"; into "each" does not match`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Apply(comboFlow, []Op{{
				Kind:   KindAddStep,
				Before: tc.anchor,
				Phase:  tc.phase,
				Into:   tc.into,
				Step:   map[string]any{"id": "new", "call": "x.y"},
			}})
			if tc.wantErrSub != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrSub)
				return
			}
			require.NoError(t, err)
			var doc map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(res.YAML), &doc))

			if tc.wantBlock == "" {
				list := doc[tc.wantHolder].([]any)
				var ids []string
				for _, s := range list {
					ids = append(ids, s.(map[string]any)["id"].(string))
				}
				assert.Contains(t, ids, "new")
			} else {
				list := doc[tc.wantHolder].([]any)
				var block map[string]any
				for _, s := range list {
					m := s.(map[string]any)
					if m["id"] == tc.wantBlock {
						block = m
					}
				}
				require.NotNil(t, block, "block %q must still be in %s", tc.wantBlock, tc.wantHolder)
				nested := block["steps"].([]any)
				var ids []string
				for _, s := range nested {
					ids = append(ids, s.(map[string]any)["id"].(string))
				}
				assert.Contains(t, ids, "new")
			}
		})
	}
}

// TestApply_AddStep_AfterAnchorNestedInferredBlock confirms `after` also
// infers a nested anchor's block, positioning the new step as that
// specific nested sibling's successor (not appended to the block).
func TestApply_AddStep_AfterAnchorNestedInferredBlock(t *testing.T) {
	res, err := Apply(comboFlow, []Op{{
		Kind:  KindAddStep,
		After: "nested",
		Step:  map[string]any{"id": "second", "call": "x.y"},
	}})
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(res.YAML), &doc))
	steps := doc["steps"].([]any)
	block := steps[1].(map[string]any)
	assert.Equal(t, "each", block["id"])
	nested := block["steps"].([]any)
	require.Len(t, nested, 2)
	assert.Equal(t, "nested", nested[0].(map[string]any)["id"])
	assert.Equal(t, "second", nested[1].(map[string]any)["id"])
}

// TestApply_AddStep_UnknownIDsListedWithLocation confirms every "unknown
// id" style error (not just add_step's anchor) names each present id's
// location, so "present but refused" (discussion #4) cannot recur anywhere
// ids are listed.
func TestApply_AddStep_UnknownIDsListedWithLocation(t *testing.T) {
	_, err := Apply(comboFlow, []Op{{Kind: KindRemoveStep, ID: "does-not-exist"}})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, `su (phase "setup")`)
	assert.Contains(t, msg, `a (phase "steps")`)
	assert.Contains(t, msg, `each (phase "steps")`)
	assert.Contains(t, msg, `nested (block "each" (phase "steps"))`)
	assert.Contains(t, msg, `td (phase "teardown")`)
}

// ---- HALF 2: layout-preserving patch application on a hand-formatted flow
//
// fmtFlow is hand-formatted the way a person actually writes a flow: 2-space
// indent, conventional key order, a comment above each step, a comment
// inside a step (above `allocate`'s `body:`), and a blank line between
// steps. Before the fix, applying ANY op here re-marshaled the whole
// document through yaml.v3's default (4-space, alphabetical-map-key)
// encoding: every one of the assertions below that checks 2-space indent or
// call-before-body-before-assert key order on an UNTOUCHED step failed on
// the old code, regardless of which op ran or which step it targeted.
const fmtFlow = `version: 1
id: fmt-demo
name: Formatting demo
steps:
  # creates the order
  - id: create
    call: order-service.createOrder
    body:
      customerId: cust_1

  # allocates a rider for it
  - id: allocate
    call: allocation-service.allocate
    # allocation must happen fast
    body:
      orderId: "${steps.create.out.orderId}"
    assert:
      - status == 201

  # confirms the allocation stuck
  - id: verify
    call: allocation-service.getAllocation
    input:
      allocationId: "${steps.allocate.out.allocationId}"
`

// untouchedAllocateBlock and untouchedVerifyBlock are exact byte spans from
// fmtFlow: an op that doesn't touch that step must reproduce them verbatim,
// comment, key order, indent, and quoting included.
const untouchedAllocateBlock = `  # allocates a rider for it
  - id: allocate
    call: allocation-service.allocate
    # allocation must happen fast
    body:
      orderId: "${steps.create.out.orderId}"
    assert:
      - status == 201
`
const untouchedVerifyBlock = `  # confirms the allocation stuck
  - id: verify
    call: allocation-service.getAllocation
    input:
      allocationId: "${steps.allocate.out.allocationId}"
`

// TestApply_HandFormatted_MergeStep_UntouchedStepsAndIndentWidthSurvive
// merges a field into `create` only, and checks the two OTHER steps come
// back byte-for-byte, and the whole file keeps fmtFlow's 2-space indent
// (yaml.v3's own default is 4, which would reflow every line, touched or
// not).
func TestApply_HandFormatted_MergeStep_UntouchedStepsAndIndentWidthSurvive(t *testing.T) {
	res, err := Apply(fmtFlow, []Op{{
		Kind:   KindMergeStep,
		ID:     "create",
		Fields: map[string]any{"until": "status == 201"},
	}})
	require.NoError(t, err)
	out := res.YAML

	assert.Contains(t, out, untouchedAllocateBlock, "the allocate step, its own comment, and its internal comment must survive verbatim")
	assert.Contains(t, out, untouchedVerifyBlock, "the verify step must survive verbatim")
	assert.NotContains(t, out, "    - id:", "sequence items must stay at the source's 2-space indent, not yaml.v3's 4-space default")
	assert.Contains(t, out, "  - id: create")
}

// TestApply_HandFormatted_MergeStep_NewKeyConventionalPositionAndComments
// merges two brand-new keys into `allocate`, out of conventional order
// (headers after timeout, alphabetically), and confirms they land at their
// OWN conventional positions regardless of processing order, the step's
// leading comment and its internal comment both survive (same node, only
// specific keys changed), and Notes says the leading comment was kept next
// to changed content.
func TestApply_HandFormatted_MergeStep_NewKeyConventionalPositionAndComments(t *testing.T) {
	res, err := Apply(fmtFlow, []Op{{
		Kind: KindMergeStep,
		ID:   "allocate",
		Fields: map[string]any{
			"timeout": "5s",
			"headers": map[string]any{"X-Test": "1"},
		},
	}})
	require.NoError(t, err)
	out := res.YAML

	assert.Contains(t, out, "# allocates a rider for it", "the step's own leading comment must survive")
	assert.Contains(t, out, "# allocation must happen fast", "the untouched body key's own comment must survive")

	// Each index is searched for starting just after the previous one, so a
	// key name that also appears earlier in the document (on `create` or
	// `verify`) can't be matched by mistake.
	callIdx := indexOfSubstring(out, "call: allocation-service.allocate")
	bodyIdx := indexOfSubstringFrom(out, "body:", callIdx)
	headersIdx := indexOfSubstringFrom(out, "headers:", bodyIdx)
	timeoutIdx := indexOfSubstringFrom(out, `timeout: 5s`, headersIdx)
	assertIdx := indexOfSubstringFrom(out, "assert:", timeoutIdx)
	require.True(t, callIdx >= 0 && bodyIdx >= 0 && headersIdx >= 0 && timeoutIdx >= 0 && assertIdx >= 0, "out:\n%s", out)
	assert.True(t, callIdx < bodyIdx && bodyIdx < headersIdx && headersIdx < timeoutIdx && timeoutIdx < assertIdx,
		"new keys must land at their conventional position (headers before timeout, both after body, both before assert), not appended in processing order: %s", out)

	require.Len(t, res.Notes, 1)
	assert.Equal(t, "step `allocate` kept its comment; check it still describes the step", res.Notes[0])
}

// TestApply_HandFormatted_SetStep_DropsOldCommentAndNotes replaces `verify`
// entirely: the OLD step's leading comment must not survive onto the new
// content in the same slot (it goes with the step it described), the other
// two steps must be untouched, and Notes must say so.
func TestApply_HandFormatted_SetStep_DropsOldCommentAndNotes(t *testing.T) {
	res, err := Apply(fmtFlow, []Op{{
		Kind: KindSetStep,
		ID:   "verify",
		Step: map[string]any{"id": "verify", "call": "allocation-service.getAllocation", "input": map[string]any{"allocationId": "x"}, "assert": []any{"status == 200"}},
	}})
	require.NoError(t, err)
	out := res.YAML

	assert.NotContains(t, out, "# confirms the allocation stuck", "the replaced step's old comment must not float onto its replacement")
	assert.Contains(t, out, untouchedAllocateBlock)
	assert.Contains(t, out, "  # creates the order")

	require.Len(t, res.Notes, 1)
	assert.Equal(t, "step `verify` had a comment above it; it was removed with the step", res.Notes[0])
}

// TestApply_HandFormatted_RemoveStep_DropsCommentAndNotes removes
// `allocate`: both its own leading comment and its internal comment leave
// with it (they're part of the same node), and Notes says so.
func TestApply_HandFormatted_RemoveStep_DropsCommentAndNotes(t *testing.T) {
	res, err := Apply(fmtFlow, []Op{{Kind: KindRemoveStep, ID: "allocate"}})
	require.NoError(t, err)
	out := res.YAML

	assert.NotContains(t, out, "# allocates a rider for it")
	assert.NotContains(t, out, "# allocation must happen fast")
	assert.Contains(t, out, "  # creates the order")
	assert.Contains(t, out, untouchedVerifyBlock)

	require.Len(t, res.Notes, 1)
	assert.Equal(t, "step `allocate` had a comment above it; it was removed with the step", res.Notes[0])
}

// TestApply_HandFormatted_AddStep_ConventionalKeyOrder adds a step whose
// JSON/map fields have no order of their own; the written step must come
// out id/call/input/assert (conventional order), never alphabetized
// (assert/call/id/input, yaml.v3's default for a map).
func TestApply_HandFormatted_AddStep_ConventionalKeyOrder(t *testing.T) {
	res, err := Apply(fmtFlow, []Op{{
		Kind: KindAddStep,
		Step: map[string]any{
			"assert": []any{"status == 200"},
			"call":   "rider-service.getRider",
			"id":     "check",
			"input":  map[string]any{"riderId": "r1"},
		},
	}})
	require.NoError(t, err)
	out := res.YAML

	idIdx := indexOfSubstring(out, "id: check")
	callIdx := indexOfSubstringFrom(out, "call: rider-service.getRider", idIdx)
	inputIdx := indexOfSubstringFrom(out, "input:", callIdx)
	assertIdx := indexOfSubstringFrom(out, "assert:\n      - status == 200", inputIdx)
	require.True(t, idIdx >= 0 && callIdx >= 0 && inputIdx >= 0 && assertIdx >= 0, "out:\n%s", out)
	assert.True(t, idIdx < callIdx && callIdx < inputIdx && inputIdx < assertIdx,
		"a new step's keys must come out in conventional order, not alphabetized: %s", out)
	assert.NotContains(t, out, "    - id: check", "the new step must also sit at the source's 2-space indent")
}

// TestApply_HandFormatted_SetInputsSetMeta_TouchOnlyOwnKeys confirms
// set_meta and set_inputs leave every step untouched (comments, key order,
// and indent all survive, same as every other op that doesn't name a
// step); only the top-level keys they own change. It does not assert
// byte-identical blank lines around the steps -- those are lost on any
// Apply call regardless of which op ran (see
// TestApply_HandFormatted_BlankLinesNotPreserved_Documented).
func TestApply_HandFormatted_SetInputsSetMeta_TouchOnlyOwnKeys(t *testing.T) {
	name := "Renamed"
	res, err := Apply(fmtFlow, []Op{
		{Kind: KindSetMeta, Meta: &Meta{Name: &name}},
		{Kind: KindSetInputs, Inputs: map[string]any{"customerId": map[string]any{"type": "string"}}},
	})
	require.NoError(t, err)
	out := res.YAML

	assert.Contains(t, out, "  # creates the order")
	assert.Contains(t, out, untouchedAllocateBlock)
	assert.Contains(t, out, untouchedVerifyBlock)
	assert.Contains(t, out, "name: Renamed")
	assert.Contains(t, out, "inputs:")
}

// TestApply_HandFormatted_BlankLinesNotPreserved_Documented is not a bug
// report: it documents (per the package doc) that yaml.v3's node tree has
// no representation for a blank line between two steps, so Apply cannot
// reproduce fmtFlow's blank line between `create` and `allocate` even
// though this op doesn't touch either step.
func TestApply_HandFormatted_BlankLinesNotPreserved_Documented(t *testing.T) {
	require.Contains(t, fmtFlow, "customerId: cust_1\n\n  # allocates a rider for it", "fixture sanity check: the blank line is really there")

	res, err := Apply(fmtFlow, []Op{{Kind: KindMergeStep, ID: "verify", Fields: map[string]any{"until": "status == 200"}}})
	require.NoError(t, err)

	assert.NotContains(t, res.YAML, "customerId: cust_1\n\n  # allocates a rider for it",
		"blank lines are not part of yaml.v3's node model; Apply does not reproduce them (documented in the package doc)")
}

// TestDetectIndentWidth exercises the heuristic directly against the cases
// the package doc promises: 2-space, 4-space, and no nested block at all
// (falls back to 2).
func TestDetectIndentWidth(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   int
	}{
		{"two space", "steps:\n  - id: a\n    call: x.y\n", 2},
		{"four space", "steps:\n    - id: a\n        call: x.y\n", 4},
		{"comment between key and first child is skipped", "steps:\n  # comment\n  - id: a\n", 2},
		{"nested mapping, not a sequence", "inputs:\n  customerId:\n    type: string\n", 2},
		{"no nested block anywhere", "version: 1\nid: x\n", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, detectIndentWidth(tc.source))
		})
	}
}
