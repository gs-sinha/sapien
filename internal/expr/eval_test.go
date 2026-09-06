package expr

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/errs"
)

func stepScope() Scope {
	return Scope{
		Inputs: map[string]any{"city": "SF", "customerId": "cust_1"},
		Env:    map[string]string{"TEST_CUSTOMER": "cust_1"},
		Steps: map[string]StepValue{
			"create": {
				Status:  201,
				Body:    map[string]any{"orderId": "ord_1", "type": "QCOM"},
				Out:     map[string]any{"orderId": "ord_1"},
				Headers: map[string]string{"content-type": "application/json"},
			},
		},
		Current: &StepValue{
			Status:  200,
			Headers: map[string]string{"content-type": "application/json"},
			Body: map[string]any{
				"riderId":   "r1",
				"online":    true,
				"count":     2.0, // simulates encoding/json's float64 decode
				"items":     []any{"a", "b"},
				"name":      "QCOM rider",
				"tags":      []any{"x", "y"},
				"counts":    []any{1, 2, 3},
				"nested":    map[string]any{"deep": map[string]any{"value": 42.0}},
				"riderNull": nil,
			},
			LatencyMs: 850.5,
			Request:   map[string]any{"method": "GET", "url": "https://svc/riders/1"},
			Out:       map[string]any{},
		},
	}
}

func TestEval_NumericCrossType(t *testing.T) {
	s := stepScope()
	e := New()

	cases := []string{
		"latency_ms < 2000",   // double < int literal
		"latency_ms > 800",    // double > int literal
		"status == 200",       // int == int
		"body.count == 2",     // normalized int64 == int literal
		"body.count < 3",      // normalized int64 < int literal
		"latency_ms <= 850.5", // double <= double
		"1 < latency_ms",      // int < double, reversed operand order
	}
	for _, c := range cases {
		v, err := e.EvalBool(c, s)
		require.NoError(t, err, c)
		assert.True(t, v, c)
	}
}

func TestEval_JSONNumberCoercion(t *testing.T) {
	// A real encoding/json decode produces float64 for every number; body.count
	// must still compare equal to an int literal after normalization.
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"count": 2, "price": 9.5, "big": 10}`), &body))

	s := Scope{Current: &StepValue{Status: 200, Body: body}}
	e := New()

	v, err := e.Eval("body.count", s)
	require.NoError(t, err)
	assert.Equal(t, int64(2), v, "integral float64 becomes int64")

	v, err = e.Eval("body.price", s)
	require.NoError(t, err)
	assert.Equal(t, 9.5, v, "fractional float64 stays a float64")

	ok, err := e.EvalBool("body.big == 10", s)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestEval_JSONNumberDecoder(t *testing.T) {
	var num json.Number = "42"
	assert.Equal(t, int64(42), normalizeJSON(num))
	num = "42.5"
	assert.Equal(t, 42.5, normalizeJSON(num))
}

func TestEval_StringFunctions(t *testing.T) {
	s := stepScope()
	e := New()
	cases := []string{
		`body.name.contains("QCOM")`,
		`body.name.matches("^QCOM")`,
		`body.name.startsWith("QCOM")`,
		`body.name.endsWith("rider")`,
		`body.items.size() == 2`,
		`size(body.items) == 2`,
	}
	for _, c := range cases {
		v, err := e.EvalBool(c, s)
		require.NoError(t, err, c)
		assert.True(t, v, c)
	}
}

func TestEval_MapListAccessAndHas(t *testing.T) {
	s := stepScope()
	e := New()

	v, err := e.Eval("body.items[0]", s)
	require.NoError(t, err)
	assert.Equal(t, "a", v)

	v, err = e.Eval("body.nested.deep.value", s)
	require.NoError(t, err)
	assert.Equal(t, int64(42), v)

	ok, err := e.EvalBool("has(body.riderId)", s)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = e.EvalBool("!has(body.missingField)", s)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestEval_NullComparisons(t *testing.T) {
	s := stepScope()
	e := New()

	ok, err := e.EvalBool("body.riderNull == null", s)
	require.NoError(t, err)
	assert.True(t, ok, "an explicit null value compares equal to null")

	ok, err = e.EvalBool("body.riderId != null", s)
	require.NoError(t, err)
	assert.True(t, ok, "a present non-null value compares unequal to null")
}

func TestEval_NestedStepReferences(t *testing.T) {
	s := stepScope()
	e := New()

	v, err := e.Eval("steps.create.body.orderId", s)
	require.NoError(t, err)
	assert.Equal(t, "ord_1", v)

	v, err = e.Eval("steps.create.out.orderId", s)
	require.NoError(t, err)
	assert.Equal(t, "ord_1", v)

	ok, err := e.EvalBool("steps.create.status == 201", s)
	require.NoError(t, err)
	assert.True(t, ok)

	v, err = e.Eval(`steps.create.headers["content-type"]`, s)
	require.NoError(t, err)
	assert.Equal(t, "application/json", v)
}

func TestEval_CurrentVsNonCurrentRoots(t *testing.T) {
	e := New()
	withCurrent := stepScope()
	noCurrent := Scope{Inputs: map[string]any{"city": "NYC"}, Env: map[string]string{}, Steps: map[string]StepValue{}}

	for _, root := range []string{"status", "headers", "body", "latency_ms", "request", "out"} {
		exprByRoot := map[string]string{
			"status":     "status == 200",
			"headers":    `headers["content-type"] == "application/json"`,
			"body":       "body.riderId == \"r1\"",
			"latency_ms": "latency_ms < 2000",
			"request":    `request.method == "GET"`,
			"out":        "size(out) == 0",
		}
		expr := exprByRoot[root]

		_, err := e.Eval(expr, withCurrent)
		assert.NoError(t, err, "root %s should work with Current set", root)

		_, err = e.Eval(expr, noCurrent)
		require.Error(t, err, "root %s should fail without Current", root)
		e2 := errs.As(err)
		assert.Equal(t, errs.Expr, e2.Code)
		assert.Contains(t, e2.Message, root)
		assert.Contains(t, e2.Message, "only available inside a step's assert/extract/until")
		assert.Contains(t, e2.Message, "steps.<id>."+root)
	}

	// inputs/env/steps remain available either way.
	_, err := e.Eval("inputs.city", noCurrent)
	assert.NoError(t, err)
}

func TestEval_UnknownVariable(t *testing.T) {
	e := New()

	_, err := e.Eval("foo.bar", stepScope())
	require.Error(t, err)
	ee := errs.As(err)
	assert.Equal(t, errs.Expr, ee.Code)
	assert.Contains(t, ee.Message, "unknown variable `foo`")
	assert.Contains(t, ee.Message, "available: inputs, env, steps, status, headers, body, latency_ms, request, out")

	noCurrent := Scope{}
	_, err = e.Eval("foo.bar", noCurrent)
	require.Error(t, err)
	ee = errs.As(err)
	assert.Contains(t, ee.Message, "available: inputs, env, steps")
	assert.NotContains(t, ee.Message, "status,")
}

func TestEval_NoSuchKey(t *testing.T) {
	e := New()
	s := stepScope()

	_, err := e.Eval("steps.create.body.missingField", s)
	require.Error(t, err)
	ee := errs.As(err)
	assert.Equal(t, errs.Expr, ee.Code)
	assert.Contains(t, ee.Message, "no such key `missingField`")
	assert.Contains(t, ee.Message, "in steps.create.body")

	_, err = e.Eval("body.alsoMissing", s)
	require.Error(t, err)
	ee = errs.As(err)
	assert.Contains(t, ee.Message, "no such key `alsoMissing`")
	assert.Contains(t, ee.Message, "in body")
}

func TestEvalBool_RequiresBool(t *testing.T) {
	e := New()
	s := stepScope()

	_, err := e.EvalBool("body.riderId", s)
	require.Error(t, err)
	ee := errs.As(err)
	assert.Equal(t, errs.Expr, ee.Code)
	assert.Contains(t, ee.Message, "must evaluate to a bool")
}

func TestEval_SyntaxError(t *testing.T) {
	e := New()
	_, err := e.Eval("status ==", stepScope())
	require.Error(t, err)
	ee := errs.As(err)
	assert.Equal(t, errs.Expr, ee.Code)
	assert.Equal(t, "status ==", ee.Details["expr"])
}

func TestEval_CachesCompiledPrograms(t *testing.T) {
	e := New()
	s := stepScope()
	_, err := e.Eval("status == 200", s)
	require.NoError(t, err)
	e.mu.RLock()
	_, ok := e.cache["status == 200"]
	e.mu.RUnlock()
	assert.True(t, ok, "successful compilations are cached")
}

func TestEval_ConcurrentUse(t *testing.T) {
	e := New()
	s := Scope{Current: &StepValue{Status: 200, LatencyMs: 100, Body: map[string]any{"n": 1}}}

	const n = 100
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := e.EvalBool("status == 200 && latency_ms < 2000 && body.n == 1", s)
			if err != nil {
				errCh <- err
				return
			}
			if !ok {
				errCh <- errs.New(errs.Internal, "expected true")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestDescribe(t *testing.T) {
	s := stepScope()

	actual, ok := Describe("status == 200", s)
	assert.True(t, ok)
	assert.Equal(t, int64(200), actual)

	actual, ok = Describe("body.riderId == \"r1\"", s)
	assert.True(t, ok)
	assert.Equal(t, "r1", actual)

	actual, ok = Describe("latency_ms < 2000", s)
	assert.True(t, ok)
	assert.Equal(t, 850.5, actual)

	_, ok = Describe("has(body.riderId)", s)
	assert.False(t, ok, "not a binary comparison")

	_, ok = Describe("status ===", s)
	assert.False(t, ok, "invalid syntax")
}
