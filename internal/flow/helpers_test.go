package flow

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/growsimplee/sapien/internal/domain"
)

func TestIsProvided(t *testing.T) {
	pathParam := domain.Param{Name: "riderId", In: domain.InPath, Required: true}
	queryParam := domain.Param{Name: "limit", In: domain.InQuery, Required: true}
	headerParam := domain.Param{Name: "X-Trace-Id", In: domain.InHeader, Required: true}

	cases := []struct {
		name string
		p    domain.Param
		st   domain.Step
		want bool
	}{
		{"path via input", pathParam, domain.Step{Input: map[string]any{"riderId": "r1"}}, true},
		{"path via params.path", pathParam, domain.Step{Params: &domain.ExplicitParams{Path: map[string]any{"riderId": "r1"}}}, true},
		{"path missing", pathParam, domain.Step{}, false},
		{"query via input", queryParam, domain.Step{Input: map[string]any{"limit": 5}}, true},
		{"query via params.query", queryParam, domain.Step{Params: &domain.ExplicitParams{Query: map[string]any{"limit": 5}}}, true},
		{"query missing", queryParam, domain.Step{}, false},
		{"header via input case-insensitive", headerParam, domain.Step{Input: map[string]any{"x-trace-id": "abc"}}, true},
		{"header via params.headers case-insensitive", headerParam, domain.Step{Params: &domain.ExplicitParams{Headers: map[string]string{"X-TRACE-ID": "abc"}}}, true},
		{"header via headers block case-insensitive", headerParam, domain.Step{Headers: map[string]string{"x-trace-id": "abc"}}, true},
		{"header via canonicalised headers block", headerParam, domain.Step{Headers: map[string]string{"X-Trace-Id": "abc"}}, true},
		{"header missing", headerParam, domain.Step{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isProvided(tc.p, tc.st))
		})
	}
}

func TestBuildParamMaps(t *testing.T) {
	op := &domain.Operation{Params: []domain.Param{
		{Name: "riderId", In: domain.InPath},
		{Name: "limit", In: domain.InQuery},
		{Name: "X-Trace-Id", In: domain.InHeader},
	}}
	pm := buildParamMaps(op)
	_, ok := pm.path["riderId"]
	assert.True(t, ok)
	_, ok = pm.query["limit"]
	assert.True(t, ok)
	_, ok = pm.header["x-trace-id"]
	assert.True(t, ok)

	assert.True(t, pm.matchesAny("riderId"))
	assert.True(t, pm.matchesAny("limit"))
	assert.True(t, pm.matchesAny("X-Trace-Id"))
	assert.False(t, pm.matchesAny("bogus"))
}

func TestParamType(t *testing.T) {
	assert.Equal(t, "any", paramType(domain.Param{}))
	assert.Equal(t, "string", paramType(domain.Param{Schema: &domain.Schema{Kind: domain.KindString}}))
}

func TestTemplates(t *testing.T) {
	assert.Equal(t, []string{"inputs.city"}, templates("${inputs.city}"))
	assert.Equal(t, []string{"inputs.city"}, templates("prefix ${inputs.city} suffix"))
	assert.Nil(t, templates("no templates here"))
	// $${ escapes to a literal "${" and is not itself a template.
	assert.Nil(t, templates("$${not-an-expr}"))
	// A brace inside a quoted string literal doesn't end the expression early.
	assert.Equal(t, []string{`"}".size()`}, templates(`${"}".size()}`))
	// An unterminated `${` yields no template (best effort).
	assert.Nil(t, templates("${unterminated"))
}

func TestCollectTemplates(t *testing.T) {
	v := map[string]any{
		"a": "${inputs.x}",
		"b": []any{"${inputs.y}", 42, true, nil},
		"c": map[string]any{"d": "${inputs.z}"},
	}
	got := collectTemplates(v)
	assert.ElementsMatch(t, []string{"inputs.x", "inputs.y", "inputs.z"}, got)
	assert.Nil(t, collectTemplates(42))
	assert.Nil(t, collectTemplates(nil))
}

func TestReferencedSteps(t *testing.T) {
	st := domain.Step{
		ID:      "check",
		Call:    "rider-service.getRider",
		Input:   map[string]any{"riderId": "${steps.allocate.out.riderId}"},
		Body:    map[string]any{"x": "${steps.create.out.orderId}"},
		Headers: map[string]string{"X-Trace": "${steps.create.body.id}"},
		Until:   "steps.allocate.status == 200",
		Extract: map[string]string{"y": "steps.rider.body.name"},
		Assert: []domain.Assertion{
			{Expr: "steps.allocate.out.riderId != null"},
			{Path: "body.x", Eq: "${steps.setup1.out.z}"},
		},
	}
	got := ReferencedSteps(st)
	assert.ElementsMatch(t, []string{"allocate", "create", "rider", "setup1"}, got)
	assert.Nil(t, ReferencedSteps(domain.Step{ID: "a", Call: "svc.op"}))
}

func TestStringMapToAny(t *testing.T) {
	assert.Nil(t, stringMapToAny(nil))
	got := stringMapToAny(map[string]string{"a": "b"})
	assert.Equal(t, map[string]any{"a": "b"}, got)
}

func TestSortedKeys(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, sortedKeys(map[string]bool{"c": true, "a": true, "b": true}))
	assert.Empty(t, sortedKeys(nil))
}

func TestNormalizeArrayPath(t *testing.T) {
	assert.Equal(t, []string{"items[]", "riderId"}, normalizeArrayPath([]string{"items", "0", "riderId"}))
	assert.Equal(t, []string{"rider", "qcomSkill"}, normalizeArrayPath([]string{"rider", "qcomSkill"}))
}

func TestIsDigits(t *testing.T) {
	assert.True(t, isDigits("0"))
	assert.True(t, isDigits("123"))
	assert.False(t, isDigits(""))
	assert.False(t, isDigits("12a"))
}
