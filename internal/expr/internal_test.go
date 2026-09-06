package expr

// White-box tests exercising unexported helpers directly, to round out
// coverage on branches that are hard to reach only through the public API.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

func TestStringify_AllKinds(t *testing.T) {
	assert.Equal(t, "", stringify(nil))
	assert.Equal(t, "true", stringify(true))
	assert.Equal(t, "false", stringify(false))
	assert.Equal(t, "5", stringify(int64(5)))
	assert.Equal(t, "5", stringify(int(5)))
	assert.Equal(t, "5", stringify(5.0))
	assert.Equal(t, "5.5", stringify(5.5))
	assert.Equal(t, "hi", stringify("hi"))
	assert.Equal(t, `{"a":1}`, stringify(map[string]any{"a": int64(1)}))
	assert.Equal(t, `["a","b"]`, stringify([]any{"a", "b"}))
}

func TestJsonOrFmt(t *testing.T) {
	assert.Equal(t, "5", jsonOrFmt(5))
	assert.Equal(t, "", jsonOrFmt(make(chan int)))
}

func TestCelLiteral_MarshalError(t *testing.T) {
	_, err := celLiteral(make(chan int))
	assert.Error(t, err)
	assert.Equal(t, errs.FlowInvalid, errs.CodeOf(err))

	_, err = CompileAssertion(domain.Assertion{Path: "body.x", Eq: make(chan int)})
	assert.Error(t, err)
	assert.Equal(t, errs.FlowInvalid, errs.CodeOf(err))
}

func TestDescribe_LeftSideFailsToEvaluate(t *testing.T) {
	_, ok := Describe("foo.bar == 1", Scope{})
	assert.False(t, ok)
}

func TestRoots_ListMapAndComprehension(t *testing.T) {
	got := Roots(`[inputs.a, inputs.b]`)
	assert.ElementsMatch(t, []Ref{{Root: "inputs", Path: []string{"a"}}, {Root: "inputs", Path: []string{"b"}}}, got)

	got = Roots(`{"k": inputs.a}`)
	assert.ElementsMatch(t, []Ref{{Root: "inputs", Path: []string{"a"}}}, got)

	got = Roots(`body.items.exists(x, x == inputs.needle)`)
	assert.Contains(t, got, Ref{Root: "body", Path: []string{"items"}})
	assert.Contains(t, got, Ref{Root: "inputs", Path: []string{"needle"}})
}

func TestVisitRefs_SelectOnCallResult(t *testing.T) {
	// The base of the select chain (foo(...)) is a call, not a plain ident,
	// so collectPath can't resolve a maximal chain for the outer select;
	// visitRefs must still recurse into the call's operand/args to find
	// inputs.a.
	got := Roots(`foo(inputs.a).bar`)
	assert.ElementsMatch(t, []Ref{{Root: "inputs", Path: []string{"a"}}}, got)
}
