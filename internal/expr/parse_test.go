package expr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/errs"
)

func TestParse_Valid(t *testing.T) {
	for _, e := range []string{
		"status == 200",
		"body.riderId == \"r1\" && has(body.online)",
		"steps.create.body.orderId",
		"1 + 2 * 3",
	} {
		assert.NoError(t, Parse(e), e)
	}
}

func TestParse_SyntaxError(t *testing.T) {
	err := Parse("status ==")
	require.Error(t, err)
	ee := errs.As(err)
	assert.Equal(t, errs.Expr, ee.Code)
	assert.Equal(t, "status ==", ee.Details["expr"])
	_, hasPos := ee.Details["position"]
	assert.True(t, hasPos, "a syntax error should carry a position")
}

func TestParse_UnmatchedParen(t *testing.T) {
	err := Parse("(status == 200")
	require.Error(t, err)
	assert.Equal(t, errs.Expr, errs.CodeOf(err))
}

func TestRoots(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want []Ref
	}{
		{
			name: "step body path",
			expr: "steps.create.body.orderId",
			want: []Ref{{Root: "steps", StepID: "create", Path: []string{"body", "orderId"}}},
		},
		{
			name: "current body path",
			expr: "body.online",
			want: []Ref{{Root: "body", Path: []string{"online"}}},
		},
		{
			name: "whole step status",
			expr: "steps.allocate.status == 200",
			want: []Ref{{Root: "steps", StepID: "allocate", Path: []string{"status"}}},
		},
		{
			name: "multiple roots deduped",
			expr: "body.riderId == body.riderId && inputs.city == inputs.city",
			want: []Ref{
				{Root: "body", Path: []string{"riderId"}},
				{Root: "inputs", Path: []string{"city"}},
			},
		},
		{
			name: "has macro",
			expr: "has(body.riderId)",
			want: []Ref{{Root: "body", Path: []string{"riderId"}}},
		},
		{
			name: "index path",
			expr: "body.items[0]",
			want: []Ref{{Root: "body", Path: []string{"items", "0"}}},
		},
		{
			name: "nested steps and inputs in one expression",
			expr: "steps.create.body.orderId == inputs.orderId",
			want: []Ref{
				{Root: "steps", StepID: "create", Path: []string{"body", "orderId"}},
				{Root: "inputs", Path: []string{"orderId"}},
			},
		},
		{
			name: "bare steps root has no step id",
			expr: "steps != null",
			want: []Ref{{Root: "steps"}},
		},
		{
			name: "env var",
			expr: "env.TOKEN == \"x\"",
			want: []Ref{{Root: "env", Path: []string{"TOKEN"}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Roots(tc.expr)
			assert.ElementsMatch(t, tc.want, got)
		})
	}
}

func TestRoots_InvalidExprReturnsNil(t *testing.T) {
	assert.Nil(t, Roots("status =="))
}
