package expr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

func f64(f float64) *float64 { return &f }
func boolp(b bool) *bool     { return &b }
func intp(i int) *int        { return &i }

func TestCompileAssertion_Bare(t *testing.T) {
	c, err := CompileAssertion(domain.Assertion{Expr: "status == 200", Message: "must be 200"})
	require.NoError(t, err)
	assert.Equal(t, Compiled{Expr: "status == 200", Kind: "cel", Message: "must be 200"}, c)
}

func TestCompileAssertion_Status(t *testing.T) {
	c, err := CompileAssertion(domain.Assertion{Status: intp(201)})
	require.NoError(t, err)
	assert.Equal(t, "status == 201", c.Expr)
	assert.Equal(t, "cel", c.Kind)
}

func TestCompileAssertion_LatencyMsSingleBound(t *testing.T) {
	c, err := CompileAssertion(domain.Assertion{LatencyMs: &domain.Range{Lt: f64(2000)}})
	require.NoError(t, err)
	assert.Equal(t, "latency_ms < 2000.0", c.Expr)
}

func TestCompileAssertion_LatencyMsCombinesBounds(t *testing.T) {
	c, err := CompileAssertion(domain.Assertion{LatencyMs: &domain.Range{Gte: f64(10), Lte: f64(2000)}})
	require.NoError(t, err)
	assert.Equal(t, "latency_ms <= 2000.0 && latency_ms >= 10.0", c.Expr)
}

func TestCompileAssertion_LatencyMsEmpty(t *testing.T) {
	_, err := CompileAssertion(domain.Assertion{LatencyMs: &domain.Range{}})
	require.Error(t, err)
	assert.Equal(t, errs.FlowInvalid, errs.CodeOf(err))
}

func TestCompileAssertion_Schema(t *testing.T) {
	c, err := CompileAssertion(domain.Assertion{Schema: "contract"})
	require.NoError(t, err)
	assert.Equal(t, Compiled{Expr: "schema:contract", Kind: "schema"}, c)
}

func TestCompileAssertion_PathEq(t *testing.T) {
	cases := []struct {
		name string
		eq   any
		want string
	}{
		{"string", "r1", `body.riderId == "r1"`},
		{"int", 5, "body.riderId == 5"},
		{"bool", true, "body.riderId == true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := CompileAssertion(domain.Assertion{Path: "body.riderId", Eq: tc.eq})
			require.NoError(t, err)
			assert.Equal(t, tc.want, c.Expr)
		})
	}
}

// Note: domain.Assertion represents Eq/Neq as `any`, so a literal Go nil is
// indistinguishable from the field being unset; `eq: null`/`neq: null`
// therefore cannot be expressed through the structured form. Use a bare
// `expr: "body.riderId == null"` (or `!= null`) instead.
func TestCompileAssertion_PathNeq(t *testing.T) {
	c, err := CompileAssertion(domain.Assertion{Path: "body.riderId", Neq: "someone-else"})
	require.NoError(t, err)
	assert.Equal(t, `body.riderId != "someone-else"`, c.Expr)
}

func TestCompileAssertion_PathExists(t *testing.T) {
	c, err := CompileAssertion(domain.Assertion{Path: "body.riderId", Exists: boolp(true), Message: "QCOM riders must have a rider"})
	require.NoError(t, err)
	assert.Equal(t, "has(body.riderId)", c.Expr)
	assert.Equal(t, "QCOM riders must have a rider", c.Message)

	c, err = CompileAssertion(domain.Assertion{Path: "body.riderId", Exists: boolp(false)})
	require.NoError(t, err)
	assert.Equal(t, "!has(body.riderId)", c.Expr)
}

func TestCompileAssertion_ExistsRequiresTwoSegments(t *testing.T) {
	_, err := CompileAssertion(domain.Assertion{Path: "body", Exists: boolp(true)})
	require.Error(t, err)
	assert.Equal(t, errs.FlowInvalid, errs.CodeOf(err))
}

func TestCompileAssertion_ExistsIndexPath(t *testing.T) {
	c, err := CompileAssertion(domain.Assertion{Path: "body.items[0]", Exists: boolp(true)})
	require.NoError(t, err)
	assert.Equal(t, "size(body.items) > 0", c.Expr)

	c, err = CompileAssertion(domain.Assertion{Path: "body.items[0]", Exists: boolp(false)})
	require.NoError(t, err)
	assert.Equal(t, "size(body.items) <= 0", c.Expr)
}

func TestCompileAssertion_PathMatches(t *testing.T) {
	c, err := CompileAssertion(domain.Assertion{Path: "body.name", Matches: `^Q.*M$`})
	require.NoError(t, err)
	assert.Equal(t, `body.name.matches("^Q.*M$")`, c.Expr)
}

func TestCompileAssertion_PathContainsString(t *testing.T) {
	c, err := CompileAssertion(domain.Assertion{Path: "body.name", Contains: "QCOM"})
	require.NoError(t, err)
	assert.Equal(t, `body.name.contains("QCOM")`, c.Expr)
}

func TestCompileAssertion_PathContainsNonString(t *testing.T) {
	c, err := CompileAssertion(domain.Assertion{Path: "body.tags", Contains: 5})
	require.NoError(t, err)
	assert.Equal(t, "5 in body.tags", c.Expr)
}

func TestCompileAssertion_Empty(t *testing.T) {
	_, err := CompileAssertion(domain.Assertion{})
	require.Error(t, err)
	assert.Equal(t, errs.FlowInvalid, errs.CodeOf(err))
}

func TestCompileAssertion_AmbiguousTopLevel(t *testing.T) {
	_, err := CompileAssertion(domain.Assertion{Status: intp(200), Path: "body.x", Eq: 1})
	require.Error(t, err)
	assert.Equal(t, errs.FlowInvalid, errs.CodeOf(err))
}

func TestCompileAssertion_PathWithoutOperator(t *testing.T) {
	_, err := CompileAssertion(domain.Assertion{Path: "body.x"})
	require.Error(t, err)
	assert.Equal(t, errs.FlowInvalid, errs.CodeOf(err))
}

func TestCompileAssertion_PathAmbiguousOperators(t *testing.T) {
	_, err := CompileAssertion(domain.Assertion{Path: "body.x", Eq: 1, Neq: 2})
	require.Error(t, err)
	assert.Equal(t, errs.FlowInvalid, errs.CodeOf(err))
}

// Every compiled CEL assertion must actually evaluate against a scope.
func TestCompileAssertion_CompiledExprsEvaluate(t *testing.T) {
	e := New()
	s := stepScope()

	tt := []domain.Assertion{
		{Status: intp(200)},
		{LatencyMs: &domain.Range{Lt: f64(2000)}},
		{Path: "body.riderId", Eq: "r1"},
		{Path: "body.riderId", Neq: "someone-else"},
		{Path: "body.riderId", Exists: boolp(true)},
		{Path: "body.items[0]", Exists: boolp(true)},
		{Path: "body.name", Matches: "QCOM"},
		{Path: "body.name", Contains: "QCOM"},
		{Path: "body.counts", Contains: 2},
	}
	for _, a := range tt {
		c, err := CompileAssertion(a)
		require.NoError(t, err)
		ok, err := e.EvalBool(c.Expr, s)
		require.NoError(t, err, c.Expr)
		assert.True(t, ok, c.Expr)
	}
}
