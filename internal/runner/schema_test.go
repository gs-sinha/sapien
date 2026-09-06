package runner

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/growsimplee/sapien/internal/domain"
)

func TestValidateSchema(t *testing.T) {
	riderSchema := &domain.Schema{
		Kind: domain.KindObject,
		Properties: map[string]*domain.Schema{
			"riderId":   {Kind: domain.KindString},
			"online":    {Kind: domain.KindBoolean},
			"qcomSkill": {Kind: domain.KindBoolean},
			"trips":     {Kind: domain.KindInteger},
			"rating":    {Kind: domain.KindNumber, Nullable: true},
			"status":    {Kind: domain.KindString, Enum: []any{"ONLINE", "OFFLINE"}},
			"vehicle": {
				Kind: domain.KindOneOf,
				Variants: []*domain.Schema{
					{Kind: domain.KindObject, Properties: map[string]*domain.Schema{
						"kind": {Kind: domain.KindString, Enum: []any{"bike"}},
					}, Required: []string{"kind"}},
					{Kind: domain.KindObject, Properties: map[string]*domain.Schema{
						"kind":       {Kind: domain.KindString, Enum: []any{"van"}},
						"capacityKg": {Kind: domain.KindNumber},
					}, Required: []string{"kind", "capacityKg"}},
				},
			},
			"tags": {
				Kind:  domain.KindArray,
				Items: &domain.Schema{Kind: domain.KindString},
			},
			"nested": {
				Kind: domain.KindObject,
				Properties: map[string]*domain.Schema{
					"items": {
						Kind: domain.KindArray,
						Items: &domain.Schema{
							Kind: domain.KindObject,
							Properties: map[string]*domain.Schema{
								"c": {Kind: domain.KindBoolean},
							},
						},
					},
				},
			},
			"extra": {Kind: domain.KindAny},
		},
		Required:             []string{"riderId", "online", "qcomSkill"},
		AdditionalProperties: nil,
	}

	tests := []struct {
		name     string
		schema   *domain.Schema
		value    any
		wantErrs int // -1 means "just check non-empty/empty via wantValid"
		valid    bool
	}{
		{
			name:   "valid rider",
			schema: riderSchema,
			value: map[string]any{
				"riderId": "R1", "online": true, "qcomSkill": true,
			},
			valid: true,
		},
		{
			name:   "not an object",
			schema: riderSchema,
			value:  "not-an-object",
			valid:  false,
		},
		{
			name:   "missing required",
			schema: riderSchema,
			value:  map[string]any{"riderId": "R1"},
			valid:  false,
		},
		{
			name:   "wrong type for property",
			schema: riderSchema,
			value: map[string]any{
				"riderId": "R1", "online": "true", "qcomSkill": true,
			},
			valid: false,
		},
		{
			name:   "nullable rating accepts null",
			schema: riderSchema,
			value: map[string]any{
				"riderId": "R1", "online": true, "qcomSkill": true, "rating": nil,
			},
			valid: true,
		},
		{
			name:   "non-nullable field rejects null",
			schema: riderSchema,
			value: map[string]any{
				"riderId": "R1", "online": nil, "qcomSkill": true,
			},
			valid: false,
		},
		{
			name:   "integer accepts integral float64",
			schema: riderSchema,
			value: map[string]any{
				"riderId": "R1", "online": true, "qcomSkill": true, "trips": float64(3),
			},
			valid: true,
		},
		{
			name:   "integer rejects fractional float64",
			schema: riderSchema,
			value: map[string]any{
				"riderId": "R1", "online": true, "qcomSkill": true, "trips": 3.5,
			},
			valid: false,
		},
		{
			name:   "enum match",
			schema: riderSchema,
			value: map[string]any{
				"riderId": "R1", "online": true, "qcomSkill": true, "status": "ONLINE",
			},
			valid: true,
		},
		{
			name:   "enum mismatch",
			schema: riderSchema,
			value: map[string]any{
				"riderId": "R1", "online": true, "qcomSkill": true, "status": "PAUSED",
			},
			valid: false,
		},
		{
			name:   "oneOf matches bike variant",
			schema: riderSchema,
			value: map[string]any{
				"riderId": "R1", "online": true, "qcomSkill": true,
				"vehicle": map[string]any{"kind": "bike"},
			},
			valid: true,
		},
		{
			name:   "oneOf matches van variant",
			schema: riderSchema,
			value: map[string]any{
				"riderId": "R1", "online": true, "qcomSkill": true,
				"vehicle": map[string]any{"kind": "van", "capacityKg": 200.0},
			},
			valid: true,
		},
		{
			name:   "oneOf matches no variant",
			schema: riderSchema,
			value: map[string]any{
				"riderId": "R1", "online": true, "qcomSkill": true,
				"vehicle": map[string]any{"kind": "car"},
			},
			valid: false,
		},
		{
			name:   "array of strings valid",
			schema: riderSchema,
			value: map[string]any{
				"riderId": "R1", "online": true, "qcomSkill": true,
				"tags": []any{"a", "b"},
			},
			valid: true,
		},
		{
			name:   "array of strings invalid item",
			schema: riderSchema,
			value: map[string]any{
				"riderId": "R1", "online": true, "qcomSkill": true,
				"tags": []any{"a", 5},
			},
			valid: false,
		},
		{
			name:   "nested path in problem message",
			schema: riderSchema,
			value: map[string]any{
				"riderId": "R1", "online": true, "qcomSkill": true,
				"nested": map[string]any{
					"items": []any{
						map[string]any{"c": true},
						map[string]any{"c": "nope"},
					},
				},
			},
			valid: false,
		},
		{
			name:   "any kind accepts everything",
			schema: &domain.Schema{Kind: domain.KindAny},
			value:  42,
			valid:  true,
		},
		{
			name:   "ref kind accepts everything",
			schema: &domain.Schema{Kind: domain.KindRef, Ref: "svc.Foo"},
			value:  map[string]any{"whatever": true},
			valid:  true,
		},
		{
			name:   "null kind accepts nil",
			schema: &domain.Schema{Kind: domain.KindNull},
			value:  nil,
			valid:  true,
		},
		{
			name:   "null kind rejects non-nil",
			schema: &domain.Schema{Kind: domain.KindNull},
			value:  "x",
			valid:  false,
		},
		{
			name: "allOf requires every variant",
			schema: &domain.Schema{Kind: domain.KindAllOf, Variants: []*domain.Schema{
				{Kind: domain.KindObject, Required: []string{"a"}},
				{Kind: domain.KindObject, Required: []string{"b"}},
			}},
			value: map[string]any{"a": 1},
			valid: false,
		},
		{
			name: "allOf passes when every variant passes",
			schema: &domain.Schema{Kind: domain.KindAllOf, Variants: []*domain.Schema{
				{Kind: domain.KindObject, Required: []string{"a"}},
				{Kind: domain.KindObject, Required: []string{"b"}},
			}},
			value: map[string]any{"a": 1, "b": 2},
			valid: true,
		},
		{
			name:   "additional properties allowed by default",
			schema: &domain.Schema{Kind: domain.KindObject},
			value:  map[string]any{"anything": "goes"},
			valid:  true,
		},
		{
			name: "additional properties validated against schema",
			schema: &domain.Schema{
				Kind:                 domain.KindObject,
				AdditionalProperties: &domain.Schema{Kind: domain.KindBoolean},
			},
			value: map[string]any{"flag": "not-a-bool"},
			valid: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			problems := ValidateSchema(tc.schema, tc.value)
			if tc.valid {
				assert.Empty(t, problems, "expected no problems, got %v", problems)
			} else {
				assert.NotEmpty(t, problems, "expected problems for %s", tc.name)
			}
		})
	}
}

func TestValidateSchema_NilSchema(t *testing.T) {
	assert.Nil(t, ValidateSchema(nil, "anything"))
}

func TestValidateSchema_ProblemPathFormat(t *testing.T) {
	s := &domain.Schema{
		Kind: domain.KindObject,
		Properties: map[string]*domain.Schema{
			"rider": {
				Kind: domain.KindObject,
				Properties: map[string]*domain.Schema{
					"online": {Kind: domain.KindBoolean},
				},
			},
		},
	}
	problems := ValidateSchema(s, map[string]any{
		"rider": map[string]any{"online": "not-a-bool"},
	})
	assert.Equal(t, []string{"body.rider.online: expected boolean, got string"}, problems)
}

func TestValidateSchema_ArrayIndexPath(t *testing.T) {
	s := &domain.Schema{Kind: domain.KindArray, Items: &domain.Schema{Kind: domain.KindString}}
	problems := ValidateSchema(s, []any{"a", 1})
	assert.Equal(t, []string{"body[1]: expected string, got integer"}, problems)
}

func TestValidateSchema_MaxFiveProblemsUpstreamJoins(t *testing.T) {
	// Not this package's concern (assertion-message truncation lives in
	// evalAssertion), but confirm ValidateSchema itself returns every
	// problem so the caller can truncate.
	s := &domain.Schema{
		Kind: domain.KindObject,
		Required: []string{
			"a", "b", "c", "d", "e", "f",
		},
	}
	problems := ValidateSchema(s, map[string]any{})
	assert.Len(t, problems, 6)
}
