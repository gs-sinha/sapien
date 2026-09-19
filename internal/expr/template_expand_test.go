package expr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/errs"
)

func TestExpandTemplates_NoTemplateIsUnchanged(t *testing.T) {
	for _, src := range []string{
		"status == 200",
		`body.riderId == "r1"`,
		"",
		"steps.create.body.orderId",
	} {
		got, err := ExpandTemplates(src)
		require.NoError(t, err)
		assert.Equal(t, src, got)
	}
}

// TestExpandTemplates_WholeStringLiteral covers rule 1: a string literal
// whose entire content is one template, "${e}" or '${e}', becomes (e) --
// quotes dropped, native type kept.
func TestExpandTemplates_WholeStringLiteral(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`body.orderId != "${steps.a.out.id}"`, `body.orderId != (steps.a.out.id)`},
		{`body.createdAt > "${steps.a.out.ts}"`, `body.createdAt > (steps.a.out.ts)`},
		{`body.x == '${inputs.y}'`, `body.x == (inputs.y)`},
	}
	for _, tc := range cases {
		got, err := ExpandTemplates(tc.src)
		require.NoError(t, err, tc.src)
		assert.Equal(t, tc.want, got, tc.src)
	}
}

// TestExpandTemplates_EmbeddedInLongerLiteral covers rule 2: a template
// embedded in a longer string literal, "ord-${e}-x", becomes
// ("ord-" + string(e) + "-x").
func TestExpandTemplates_EmbeddedInLongerLiteral(t *testing.T) {
	got, err := ExpandTemplates(`"ord-${e}-x"`)
	require.NoError(t, err)
	assert.Equal(t, `("ord-" + string(e) + "-x")`, got)

	// Template at the very start/end: no empty literal segment emitted.
	got, err = ExpandTemplates(`"${a}${b}"`)
	require.NoError(t, err)
	assert.Equal(t, `(string(a) + string(b))`, got)

	got, err = ExpandTemplates(`body.name == "Hello ${inputs.name}!"`)
	require.NoError(t, err)
	assert.Equal(t, `body.name == ("Hello " + string(inputs.name) + "!")`, got)
}

// TestExpandTemplates_BareOutsideString covers rule 3: a template outside
// any string literal, body.ts > ${e}, becomes body.ts > (e).
func TestExpandTemplates_BareOutsideString(t *testing.T) {
	got, err := ExpandTemplates("body.ts > ${steps.a.out.ts}")
	require.NoError(t, err)
	assert.Equal(t, "body.ts > (steps.a.out.ts)", got)

	got, err = ExpandTemplates("${inputs.ok}")
	require.NoError(t, err)
	assert.Equal(t, "(inputs.ok)", got)
}

func TestExpandTemplates_EscapedDollarBraceStaysLiteral(t *testing.T) {
	got, err := ExpandTemplates(`body.x == "$${not-an-expr}"`)
	require.NoError(t, err)
	assert.Equal(t, `body.x == "${not-an-expr}"`, got)

	got, err = ExpandTemplates(`$${outside} == 1`)
	require.NoError(t, err)
	assert.Equal(t, "${outside} == 1", got)
}

// TestExpandTemplates_NestedBracesAndQuotesBalance covers a template whose
// own expression contains a map literal (nested braces) and a nested string
// (possibly containing a `}` or the outer quote character), which must not
// end the template early.
func TestExpandTemplates_NestedBracesAndQuotesBalance(t *testing.T) {
	got, err := ExpandTemplates(`"${ {"a": 1}.a }"`)
	require.NoError(t, err)
	assert.Equal(t, `( {"a": 1}.a )`, got)

	got, err = ExpandTemplates(`"prefix-${a + "b}c"}-suffix"`)
	require.NoError(t, err)
	assert.Equal(t, `("prefix-" + string(a + "b}c") + "-suffix")`, got)
}

func TestExpandTemplates_UnterminatedTemplateIsError(t *testing.T) {
	_, err := ExpandTemplates("inputs.x == ${inputs.y")
	require.Error(t, err)
	assert.Equal(t, errs.Expr, errs.CodeOf(err))
	assert.Contains(t, err.Error(), "unterminated")

	_, err = ExpandTemplates(`"${inputs.y"`)
	require.Error(t, err)
	assert.Equal(t, errs.Expr, errs.CodeOf(err))
}

// TestExpandTemplates_MalformedStringPassesThrough covers an unterminated
// string literal: ExpandTemplates leaves it (and any ${...} it already
// contains) unchanged rather than erroring, since that's a bare CEL syntax
// problem, not a templating one -- the flow validator's TEMPLATE_IN_EXPR
// check is what catches a `${` surviving this way.
func TestExpandTemplates_MalformedStringPassesThrough(t *testing.T) {
	src := `inputs.x == "${inputs.y}`
	got, err := ExpandTemplates(src)
	require.NoError(t, err)
	assert.Equal(t, src, got, "no closing quote: passed through unchanged, template included")
	assert.Contains(t, got, "${", "the template literally survives")
}

// TestExpandTemplates_SecretStaysImpossible confirms that rewriting
// ${secret.NAME} to (secret.NAME) does not make `secret` usable in a
// CEL-typed field: sharedEnv never declares a `secret` root, so compiling
// the rewritten source still fails, with the same "unknown variable" shape
// any other undeclared root gets.
func TestExpandTemplates_SecretStaysImpossible(t *testing.T) {
	got, err := ExpandTemplates("${secret.TOKEN}")
	require.NoError(t, err)
	assert.Equal(t, "(secret.TOKEN)", got)

	e := New()
	_, err = e.Eval("${secret.TOKEN}", Scope{Current: &StepValue{}})
	require.Error(t, err)
	ee := errs.As(err)
	assert.Equal(t, errs.Expr, ee.Code)
	assert.Contains(t, ee.Message, "unknown variable `secret`")
}

// TestExpandTemplates_QuoteStyleReused confirms a single-quoted literal's
// surrounding text is rebuilt with single quotes (not silently switched to
// double), so no re-escaping of the original text is ever needed.
func TestExpandTemplates_QuoteStyleReused(t *testing.T) {
	got, err := ExpandTemplates(`'pre-${e}-post'`)
	require.NoError(t, err)
	assert.Equal(t, `('pre-' + string(e) + '-post')`, got)
}
