package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestAssertion_UnmarshalYAML_BothForms(t *testing.T) {
	var f Flow
	require.NoError(t, yaml.Unmarshal([]byte(`version: 1
id: x
steps:
  - id: a
    call: svc.op
    assert:
      - status == 200
      - expr: body.ok == true
        message: must be ok
      - status: 201
      - path: body.id
        exists: true
`), &f))
	require.Len(t, f.Steps, 1)
	as := f.Steps[0].Assert
	require.Len(t, as, 4)
	assert.Equal(t, "status == 200", as[0].Expr)
	assert.Equal(t, "body.ok == true", as[1].Expr)
	assert.Equal(t, "must be ok", as[1].Message)
	require.NotNil(t, as[2].Status)
	assert.Equal(t, 201, *as[2].Status)
	assert.Equal(t, "body.id", as[3].Path)
	assert.Positive(t, as[0].Line)

	// Marshalling emits the mapping form, which unmarshals back identically.
	out, err := yaml.Marshal(f)
	require.NoError(t, err)
	var back Flow
	require.NoError(t, yaml.Unmarshal(out, &back))
	assert.Equal(t, as[0].Expr, back.Steps[0].Assert[0].Expr)
	assert.Equal(t, as[1].Message, back.Steps[0].Assert[1].Message)
}
