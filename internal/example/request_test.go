package example

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

func str(name string) *domain.Schema { return &domain.Schema{Kind: domain.KindString} }

// createOrderOp is an operation shaped like the ones onboarding produces: a
// required body with a nested object, an optional field the contract gives a
// value for, an optional one it does not, and a server-owned readOnly field.
func createOrderOp() *domain.Operation {
	return &domain.Operation{
		ID: "order-service.createOrder",
		Params: []domain.Param{
			{Name: "tenantId", In: domain.InHeader, Required: true, Schema: str("tenantId")},
			{Name: "dryRun", In: domain.InQuery, Schema: &domain.Schema{Kind: domain.KindBoolean, Default: false}},
			{Name: "trace", In: domain.InQuery, Schema: str("trace")},
		},
		RequestBody: &domain.Body{
			ContentType: "application/json",
			Required:    true,
			Schema: &domain.Schema{
				Kind:          domain.KindObject,
				Required:      []string{"customerId", "type", "pickup"},
				PropertyOrder: []string{"customerId", "type", "priority", "note", "orderId", "pickup", "items"},
				Properties: map[string]*domain.Schema{
					"customerId": str("customerId"),
					"type":       {Kind: domain.KindString, Enum: []any{"QCOM", "INTERCITY"}},
					"priority":   {Kind: domain.KindInteger, Default: 3},
					"note":       str("note"),
					"orderId":    {Kind: domain.KindString, ReadOnly: true},
					"pickup": {
						Kind: domain.KindObject, Required: []string{"lat", "lng"},
						PropertyOrder: []string{"lat", "lng", "landmark"},
						Properties: map[string]*domain.Schema{
							"lat":      {Kind: domain.KindNumber, Example: 12.9716},
							"lng":      {Kind: domain.KindNumber, Example: 77.5946},
							"landmark": str("landmark"),
						},
					},
					"items": {Kind: domain.KindArray, Items: &domain.Schema{Kind: domain.KindObject,
						Required: []string{"sku"}, PropertyOrder: []string{"sku"},
						Properties: map[string]*domain.Schema{"sku": str("sku")}}},
				},
			},
		},
	}
}

func TestResolve_SynthesizesFromSchemaWhenNothingIsSaved(t *testing.T) {
	got := Resolve(createOrderOp(), nil)

	assert.Equal(t, domain.RequestExampleSynthesized, got.Source)
	assert.Contains(t, got.Note, "placeholder")

	body, ok := got.Body.(map[string]any)
	require.True(t, ok, "body should be an object")
	assert.Equal(t, "<customerId>", body["customerId"])
	assert.Equal(t, "QCOM", body["type"], "an enum should use its first value, not a placeholder")
	assert.Equal(t, 3, body["priority"], "an optional field the contract gives a default is worth including")
	assert.NotContains(t, body, "note", "an optional field with no contract value is omitted by default")
	assert.NotContains(t, body, "orderId", "a readOnly field is the server's to set")
	assert.Equal(t, map[string]any{"lat": 12.9716, "lng": 77.5946}, body["pickup"])
	assert.NotContains(t, body, "items", "an optional array stays out of the default payload")

	// Params: required always, optional only when the contract pins a value.
	assert.Equal(t, map[string]any{"tenantId": "<tenantId>", "dryRun": false}, got.Input)
}

func TestSynthesize_IncludeOptionalFillsTheWholeShape(t *testing.T) {
	got := Synthesize(createOrderOp(), true)

	body := got.Body.(map[string]any)
	assert.Contains(t, body, "note", "includeOptional is for a human who wants everything to delete from")
	assert.Contains(t, body, "items")
	items := body["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, map[string]any{"sku": "<sku>"}, items[0])
	assert.NotContains(t, body, "orderId", "readOnly stays out even then")
	assert.Contains(t, body["pickup"].(map[string]any), "landmark")
	assert.Contains(t, got.Input, "trace")
}

func TestResolve_ContractExampleBeatsSynthesis(t *testing.T) {
	op := createOrderOp()
	op.RequestBody.Examples = []domain.Example{{Name: "qcom", Value: map[string]any{"customerId": "cust_1", "type": "QCOM"}}}

	got := Resolve(op, nil)

	assert.Equal(t, domain.RequestExampleContract, got.Source)
	assert.Equal(t, "qcom", got.SourceID)
	assert.Equal(t, map[string]any{"customerId": "cust_1", "type": "QCOM"}, got.Body)
	assert.NotEmpty(t, got.Input, "params are still synthesized: the contract example is only the body")
}

func TestResolve_SavedBeatsContractAndVerifiedBeatsSaved(t *testing.T) {
	op := createOrderOp()
	op.RequestBody.Examples = []domain.Example{{Name: "qcom", Value: map[string]any{"customerId": "contract"}}}
	handWritten := domain.SavedExample{ID: "hand", Operation: op.ID, Body: map[string]any{"customerId": "hand"}}
	verified := domain.SavedExample{
		ID: "real", Operation: op.ID, Body: map[string]any{"customerId": "real"},
		Input:    map[string]any{"tenantId": "t1"},
		Verified: &domain.ExampleVerified{Env: "staging", At: time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)},
	}

	saved := Resolve(op, []domain.SavedExample{handWritten})
	assert.Equal(t, domain.RequestExampleSaved, saved.Source)
	assert.Equal(t, "hand", saved.SourceID)
	assert.Contains(t, saved.Note, "not yet confirmed")

	// ForOperations returns verified first, but precedence must not depend on
	// the caller's ordering.
	best := Resolve(op, []domain.SavedExample{handWritten, verified})
	assert.Equal(t, domain.RequestExampleVerified, best.Source)
	assert.Equal(t, "real", best.SourceID)
	assert.Equal(t, map[string]any{"customerId": "real"}, best.Body)
	assert.Equal(t, map[string]any{"tenantId": "t1"}, best.Input)
	assert.Equal(t, "sent successfully against staging on 2026-09-12", best.Note)
}

func TestResolve_IgnoresExamplesForOtherOperations(t *testing.T) {
	op := createOrderOp()
	other := domain.SavedExample{ID: "elsewhere", Operation: "allocation-service.allocate", Body: map[string]any{"orderId": "o1"}}

	got := Resolve(op, []domain.SavedExample{other})

	assert.Equal(t, domain.RequestExampleSynthesized, got.Source)
}

func TestResolve_NoBodyOperation(t *testing.T) {
	op := &domain.Operation{
		ID:     "allocation-service.getAllocation",
		Params: []domain.Param{{Name: "allocationId", In: domain.InPath, Required: true, Schema: str("allocationId")}},
	}

	got := Resolve(op, nil)

	assert.Nil(t, got.Body)
	assert.Equal(t, map[string]any{"allocationId": "<allocationId>"}, got.Input)
}

func TestSynthesizeBody_FormatsAndCycles(t *testing.T) {
	schema := &domain.Schema{
		Kind: domain.KindObject, Required: []string{"at", "on", "who", "id", "link", "self"},
		PropertyOrder: []string{"at", "on", "who", "id", "link", "self"},
		Properties: map[string]*domain.Schema{
			"at":   {Kind: domain.KindString, Format: "date-time"},
			"on":   {Kind: domain.KindString, Format: "date"},
			"who":  {Kind: domain.KindString, Format: "email"},
			"id":   {Kind: domain.KindString, Format: "uuid"},
			"link": {Kind: domain.KindString, Format: "uri"},
			"self": {Kind: domain.KindRef, Ref: "order-service.Order"},
		},
	}

	got := SynthesizeBody(schema, false).(map[string]any)

	assert.Equal(t, "2026-01-01T00:00:00Z", got["at"])
	assert.Equal(t, "2026-01-01", got["on"])
	assert.Equal(t, "user@example.com", got["who"])
	assert.Equal(t, "00000000-0000-0000-0000-000000000000", got["id"])
	assert.Equal(t, "https://example.com", got["link"])
	assert.Equal(t, map[string]any{}, got["self"], "a cyclic ref has nothing left to expand")
}

func TestSynthesizeBody_OneOfAndAllOf(t *testing.T) {
	oneOf := &domain.Schema{Kind: domain.KindOneOf, Variants: []*domain.Schema{
		{Kind: domain.KindObject, Required: []string{"card"}, PropertyOrder: []string{"card"},
			Properties: map[string]*domain.Schema{"card": str("card")}},
		{Kind: domain.KindObject, Required: []string{"upi"}, PropertyOrder: []string{"upi"},
			Properties: map[string]*domain.Schema{"upi": str("upi")}},
	}}
	assert.Equal(t, map[string]any{"card": "<card>"}, SynthesizeBody(oneOf, false), "the first variant is the one to show")

	allOf := &domain.Schema{Kind: domain.KindAllOf, Variants: []*domain.Schema{
		{Kind: domain.KindObject, Required: []string{"a"}, PropertyOrder: []string{"a"}, Properties: map[string]*domain.Schema{"a": str("a")}},
		{Kind: domain.KindObject, Required: []string{"b"}, PropertyOrder: []string{"b"}, Properties: map[string]*domain.Schema{"b": str("b")}},
	}}
	assert.Equal(t, map[string]any{"a": "<a>", "b": "<b>"}, SynthesizeBody(allOf, false))
}

func TestSynthesizeBody_UnorderedPropertiesAreStable(t *testing.T) {
	// No PropertyOrder (a contract Sapien ingested without one): the result
	// must not depend on Go's map iteration order.
	schema := &domain.Schema{Kind: domain.KindObject, Required: []string{"a", "b", "c"},
		Properties: map[string]*domain.Schema{"c": str("c"), "a": str("a"), "b": str("b")}}

	first := SynthesizeBody(schema, false)
	for i := 0; i < 20; i++ {
		assert.Equal(t, first, SynthesizeBody(schema, false))
	}
}

func TestResolve_NilOperation(t *testing.T) {
	assert.Equal(t, domain.RequestExample{}, Resolve(nil, nil))
}

// TestSynthesize_IgnoresSavedAndContractExamples is what the UI's "whole
// shape" button depends on: a reader asking to see every field is not served
// by being handed the contract's own example, which is the payload already in
// front of them.
func TestSynthesize_IgnoresSavedAndContractExamples(t *testing.T) {
	op := createOrderOp()
	op.RequestBody.Examples = []domain.Example{{Name: "qcom", Value: map[string]any{"customerId": "cust_1"}}}

	got := Synthesize(op, true)

	assert.Equal(t, domain.RequestExampleSynthesized, got.Source)
	body := got.Body.(map[string]any)
	assert.Contains(t, body, "note", "every declared field, not the contract's two")
	assert.Equal(t, "<customerId>", body["customerId"])
}

func TestSynthesizeInput_RequiredDeprecatedParamIsStillSent(t *testing.T) {
	op := &domain.Operation{
		ID: "svc.op",
		Params: []domain.Param{
			{Name: "legacyKey", In: domain.InQuery, Required: true, Deprecated: true, Schema: str("legacyKey")},
			{Name: "oldFilter", In: domain.InQuery, Deprecated: true, Schema: str("oldFilter")},
		},
	}

	got := SynthesizeInput(op, false)

	assert.Contains(t, got, "legacyKey", "deprecated but required: leaving it out yields a request that cannot work")
	assert.NotContains(t, got, "oldFilter")
}
