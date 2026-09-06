package runner

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

func TestDefaultSleep(t *testing.T) {
	require.NoError(t, defaultSleep(context.Background(), 0))

	start := time.Now()
	require.NoError(t, defaultSleep(context.Background(), 5*time.Millisecond))
	assert.GreaterOrEqual(t, time.Since(start), 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := defaultSleep(ctx, time.Hour)
	require.Error(t, err)
}

func TestNoSecretStore(t *testing.T) {
	var s noSecretStore
	_, err := s.Get("X")
	require.Error(t, err)
	assert.Equal(t, errs.SecretMissing, errs.CodeOf(err))

	require.Error(t, s.Set("X", "v"))
	require.Error(t, s.Delete("X"))
	names, err := s.List()
	require.NoError(t, err)
	assert.Nil(t, names)
}

func TestRenderCallFlowYAML(t *testing.T) {
	bare := renderCallFlowYAML("svc.op", nil, nil, nil)
	assert.Contains(t, bare, "call: svc.op")
	assert.NotContains(t, bare, "input:")
	assert.NotContains(t, bare, "body:")
	assert.NotContains(t, bare, "headers:")

	full := renderCallFlowYAML("svc.op", map[string]any{"id": "1"}, map[string]any{"a": 1}, map[string]string{"H": "v"})
	assert.Contains(t, full, "input:")
	assert.Contains(t, full, "body:")
	assert.Contains(t, full, "headers:")
}

func TestDeriveRunError_Fallback(t *testing.T) {
	// No steps at all: DeriveStatus falls back to RunErrored ("nothing
	// meaningful actually completed"), and deriveRunError has no errored
	// step or engineErr to draw from either.
	info := deriveRunError(nil, nil)
	require.NotNil(t, info)
	assert.Equal(t, string(errs.Internal), info.Code)
}

func TestDeriveRunError_FromEngineErr(t *testing.T) {
	info := deriveRunError(nil, errs.New(errs.Internal, "boom"))
	require.NotNil(t, info)
	assert.Equal(t, "boom", info.Message)
}

func TestRun_EmptyFlow_ErrorsWithNoSteps(t *testing.T) {
	ops, e, _ := startFixtures(t)
	r := New(ops)
	f := &domain.Flow{Version: 1, ID: "empty"}
	run, err := r.Run(context.Background(), f, nil, Options{Env: e})
	require.NoError(t, err)
	assert.Equal(t, domain.RunErrored, run.Status)
	require.NotNil(t, run.Error)
	assert.Equal(t, string(errs.Internal), run.Error.Code)
}

func TestResolveDefault_NonStringPassesThrough(t *testing.T) {
	v, err := resolveDefault(42, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 42, v)
}

func TestResolveDefault_InterpolatesEnv(t *testing.T) {
	ev := newTestEvaluator()
	v, err := resolveDefault("${env.CITY}", map[string]string{"CITY": "Bangalore"}, ev)
	require.NoError(t, err)
	assert.Equal(t, "Bangalore", v)
}

func TestResolveDefault_Error(t *testing.T) {
	ev := newTestEvaluator()
	_, err := resolveDefault("${env.unknown_root}", nil, ev)
	require.Error(t, err)
}

func TestResolveInputs(t *testing.T) {
	ev := newTestEvaluator()
	f := &domain.Flow{Inputs: map[string]domain.InputSpec{
		"a": {Required: true},
		"b": {Default: "def-b"},
		"c": {}, // optional, no default -> omitted from the merged map
	}}
	merged, err := resolveInputs(f, map[string]any{"a": "override-a", "extra": "passthrough"}, nil, ev)
	require.NoError(t, err)
	assert.Equal(t, "override-a", merged["a"])
	assert.Equal(t, "def-b", merged["b"])
	_, hasC := merged["c"]
	assert.False(t, hasC)
	assert.Equal(t, "passthrough", merged["extra"])
}

func TestResolveInputs_MissingRequired(t *testing.T) {
	ev := newTestEvaluator()
	f := &domain.Flow{Inputs: map[string]domain.InputSpec{"a": {Required: true}}}
	_, err := resolveInputs(f, nil, nil, ev)
	require.Error(t, err)
	assert.Equal(t, errs.InputMissing, errs.CodeOf(err))
}

func TestResolveInputs_DefaultError(t *testing.T) {
	ev := newTestEvaluator()
	f := &domain.Flow{Inputs: map[string]domain.InputSpec{"a": {Default: "${env.unknown_root}"}}}
	_, err := resolveInputs(f, nil, nil, ev)
	require.Error(t, err)
}

func TestIsInteger(t *testing.T) {
	assert.True(t, isInteger(int32(3)))
	assert.True(t, isInteger(float32(3)))
	assert.False(t, isInteger(float32(3.5)))
	assert.False(t, isInteger("nope"))
}

func TestToFloat(t *testing.T) {
	assert.Equal(t, 3.0, toFloat(3))
	assert.Equal(t, 3.0, toFloat(int32(3)))
	assert.Equal(t, 3.0, toFloat(int64(3)))
	assert.Equal(t, 3.0, toFloat(float32(3)))
	assert.Equal(t, 3.0, toFloat(3.0))
	assert.Equal(t, 0.0, toFloat("nope"))
}

func TestTypeName(t *testing.T) {
	assert.Equal(t, "string", typeName("x"))
	assert.Equal(t, "boolean", typeName(true))
	assert.Equal(t, "integer", typeName(3))
	assert.Equal(t, "integer", typeName(int64(3)))
	assert.Equal(t, "number", typeName(3.5))
	assert.Equal(t, "object", typeName(map[string]any{}))
	assert.Equal(t, "array", typeName([]any{}))
	assert.Equal(t, "null", typeName(nil))
	type weird struct{}
	assert.Contains(t, typeName(weird{}), "weird")
}

func TestEnumMatch_NumericFallbackAndCrossType(t *testing.T) {
	assert.True(t, enumMatch(int64(5), float64(5)))
	assert.True(t, enumMatch(5, "5")) // fmt.Sprintf fallback
	assert.False(t, enumMatch(5, "6"))
}

func TestValidateArray_NoItemsSchemaAcceptsAnything(t *testing.T) {
	s := &domain.Schema{Kind: domain.KindArray}
	assert.Empty(t, ValidateSchema(s, []any{"a", 1, true, map[string]any{"x": 1}}))
}

func TestValidateAnyVariant_NoVariants(t *testing.T) {
	s := &domain.Schema{Kind: domain.KindOneOf}
	assert.Empty(t, ValidateSchema(s, "anything"))
}
