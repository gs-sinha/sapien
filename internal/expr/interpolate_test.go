package expr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/errs"
)

func TestIsTemplate(t *testing.T) {
	cases := map[string]bool{
		"${inputs.city}":       true,
		"hello ${inputs.city}": true,
		"plain string":         false,
		"$${escaped}":          false,
		"$${escaped} ${real}":  true,
	}
	for tmpl, want := range cases {
		assert.Equal(t, want, IsTemplate(tmpl), tmpl)
	}
}

func TestInterpolate_SingleExprKeepsType(t *testing.T) {
	e := New()
	s := stepScope()

	v, used, err := e.Interpolate("${inputs.city}", s)
	require.NoError(t, err)
	assert.Empty(t, used)
	assert.Equal(t, "SF", v)

	v, used, err = e.Interpolate("${body.count}", s)
	require.NoError(t, err)
	assert.Equal(t, int64(2), v, "keeps its native (int64) type, not stringified")

	v, used, err = e.Interpolate("${body.online}", s)
	require.NoError(t, err)
	assert.Equal(t, true, v)

	v, used, err = e.Interpolate("${body.items}", s)
	require.NoError(t, err)
	assert.Equal(t, []any{"a", "b"}, v)
}

func TestInterpolate_Concatenation(t *testing.T) {
	e := New()
	s := stepScope()

	v, _, err := e.Interpolate("order ${steps.create.out.orderId} for ${inputs.city}", s)
	require.NoError(t, err)
	assert.Equal(t, "order ord_1 for SF", v)

	v, _, err = e.Interpolate("count=${body.count}", s)
	require.NoError(t, err)
	assert.Equal(t, "count=2", v, "no trailing .0 for an integral float")

	v, _, err = e.Interpolate("tags=${body.tags}", s)
	require.NoError(t, err)
	assert.Equal(t, `tags=["x","y"]`, v, "maps/slices stringify as compact JSON")

	v, _, err = e.Interpolate("no expr here", s)
	require.NoError(t, err)
	assert.Equal(t, "no expr here", v)
}

func TestInterpolate_EscapedLiteral(t *testing.T) {
	e := New()
	s := stepScope()

	v, used, err := e.Interpolate("$${literal}", s)
	require.NoError(t, err)
	assert.Empty(t, used)
	assert.Equal(t, "${literal}", v)

	v, _, err = e.Interpolate("prefix $${escaped} ${inputs.city}", s)
	require.NoError(t, err)
	assert.Equal(t, "prefix ${escaped} SF", v)
}

func TestInterpolate_BracesInsideStringLiteral(t *testing.T) {
	e := New()
	s := stepScope()

	// The CEL string literal contains a `}`; it must not end the ${...} early.
	v, _, err := e.Interpolate(`${"a}b" + inputs.city}`, s)
	require.NoError(t, err)
	assert.Equal(t, "a}bSF", v)
}

func TestInterpolate_UnterminatedTemplate(t *testing.T) {
	e := New()
	_, _, err := e.Interpolate("${status", stepScope())
	require.Error(t, err)
	ee := errs.As(err)
	assert.Equal(t, errs.Expr, ee.Code)
}

func TestInterpolateValue_Nested(t *testing.T) {
	e := New()
	s := stepScope()

	in := map[string]any{
		"city": "${inputs.city}",
		"tags": []any{"${inputs.city}", "static"},
		"nested": map[string]any{
			"orderId": "${steps.create.out.orderId}",
		},
	}
	out, used, err := e.InterpolateValue(in, s)
	require.NoError(t, err)
	assert.Empty(t, used)

	m := out.(map[string]any)
	assert.Equal(t, "SF", m["city"])
	assert.Equal(t, []any{"SF", "static"}, m["tags"])
	nested := m["nested"].(map[string]any)
	assert.Equal(t, "ord_1", nested["orderId"])
}

func TestInterpolate_SecretsDeniedByDefault(t *testing.T) {
	e := New()
	s := stepScope() // AllowSecrets defaults to false

	_, _, err := e.Interpolate("Bearer ${secret.apiKey}", s)
	require.Error(t, err)
	ee := errs.As(err)
	assert.Equal(t, errs.Expr, ee.Code)
	assert.Equal(t, "secret references are only allowed in headers and auth values", ee.Message)
}

func TestInterpolate_SecretsAllowed(t *testing.T) {
	e := New()
	s := stepScope()
	s.AllowSecrets = true
	var resolved []string
	s.SecretResolver = func(name string) (string, error) {
		resolved = append(resolved, name)
		return "shh-" + name, nil
	}

	v, used, err := e.Interpolate("Bearer ${secret.apiKey}", s)
	require.NoError(t, err)
	assert.Equal(t, "Bearer shh-apiKey", v)
	assert.Equal(t, []string{"apiKey"}, used)
	assert.Equal(t, []string{"apiKey"}, resolved)
}

func TestInterpolate_SecretsMixedWithCEL(t *testing.T) {
	e := New()
	s := stepScope()
	s.AllowSecrets = true
	s.SecretResolver = func(name string) (string, error) { return "TOK", nil }

	v, used, err := e.Interpolate(`${"Bearer " + secret.apiKey}`, s)
	require.NoError(t, err)
	assert.Equal(t, "Bearer TOK", v)
	assert.Equal(t, []string{"apiKey"}, used)
}

func TestInterpolate_SecretResolverError(t *testing.T) {
	e := New()
	s := stepScope()
	s.AllowSecrets = true
	s.SecretResolver = func(name string) (string, error) {
		return "", errs.New(errs.SecretMissing, "no such secret `%s`", name)
	}

	_, _, err := e.Interpolate("${secret.apiKey}", s)
	require.Error(t, err)
	assert.Equal(t, errs.SecretMissing, errs.CodeOf(err))
}
