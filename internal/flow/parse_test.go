package flow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

const lineNumberFlow = `version: 1
id: t
steps:
  - id: a
    call: order-service.createOrder
    body: {x: 1}
    assert:
      - status == 201
      - { path: body.x, eq: 1 }
  - id: b
    call: allocation-service.allocate
    assert:
      - status == 200
`

func TestParse_LineNumbers(t *testing.T) {
	f, err := Parse(lineNumberFlow)
	require.NoError(t, err)
	require.Len(t, f.Steps, 2)

	stepA := f.Steps[0]
	assert.Equal(t, "a", stepA.ID)
	assert.Equal(t, 4, stepA.Line)
	require.Len(t, stepA.Assert, 2)
	assert.Equal(t, 8, stepA.Assert[0].Line)
	assert.Equal(t, "status == 201", stepA.Assert[0].Expr)
	assert.Equal(t, 9, stepA.Assert[1].Line)
	assert.Equal(t, "body.x", stepA.Assert[1].Path)
	assert.EqualValues(t, 1, stepA.Assert[1].Eq)

	stepB := f.Steps[1]
	assert.Equal(t, "b", stepB.ID)
	assert.Equal(t, 10, stepB.Line)
	require.Len(t, stepB.Assert, 1)
	assert.Equal(t, 13, stepB.Assert[0].Line)
}

func TestParse_AssertScalarForm(t *testing.T) {
	f, err := Parse(lineNumberFlow)
	require.NoError(t, err)
	a := f.Steps[0].Assert[0]
	assert.Equal(t, "status == 201", a.Expr)
	assert.Empty(t, a.Path)
	assert.Nil(t, a.Status)
}

func TestParse_AssertMappingForm(t *testing.T) {
	f, err := Parse(lineNumberFlow)
	require.NoError(t, err)
	a := f.Steps[0].Assert[1]
	assert.Empty(t, a.Expr)
	assert.Equal(t, "body.x", a.Path)
	assert.EqualValues(t, 1, a.Eq)
}

func TestParse_SetsSource(t *testing.T) {
	f, err := Parse(lineNumberFlow)
	require.NoError(t, err)
	assert.Equal(t, lineNumberFlow, f.Source)
}

func TestParse_MalformedYAML(t *testing.T) {
	f, err := Parse("version: 1\nsteps: [\n")
	require.Error(t, err)
	assert.Nil(t, f)
	e := errs.As(err)
	assert.Equal(t, errs.FlowInvalid, e.Code)
	diags, ok := e.Details["diagnostics"].([]domain.Diagnostic)
	require.True(t, ok)
	require.NotEmpty(t, diags)
}

func TestParse_ReservedKey(t *testing.T) {
	src := readExample(t, "flow-reserved-parallel.invalid.yaml")
	f, err := Parse(src)
	require.Error(t, err)
	assert.Nil(t, f)
	diags := diagnosticsOf(t, err)
	require.NotEmpty(t, diags)
	found := findCode(diags, CodeReservedKey)
	require.NotNil(t, found, "expected a RESERVED_KEY diagnostic, got %+v", diags)
	assert.Equal(t, domain.SeverityError, found.Severity)
	assert.Positive(t, found.Line)
}

// TestParse_When confirms `when` (PLAN §34f.7) parses onto domain.Step.When
// and is no longer rejected as a reserved key.
func TestParse_When(t *testing.T) {
	f, err := Parse(`version: 1
id: t
steps:
  - id: a
    call: order-service.createOrder
    when: inputs.releaseNow
    body: {x: 1}
`)
	require.NoError(t, err)
	require.Len(t, f.Steps, 1)
	assert.Equal(t, "inputs.releaseNow", f.Steps[0].When)
}

func TestParse_UnknownAssertionKey(t *testing.T) {
	src := readExample(t, "flow-bad-assertion-key.invalid.yaml")
	_, err := Parse(src)
	require.Error(t, err)
	diags := diagnosticsOf(t, err)
	found := findCode(diags, CodeUnknownKey)
	require.NotNil(t, found, "expected an UNKNOWN_KEY diagnostic, got %+v", diags)
}

func TestParse_BadCallPattern(t *testing.T) {
	src := readExample(t, "flow-bad-call-pattern.invalid.yaml")
	_, err := Parse(src)
	require.Error(t, err)
	diags := diagnosticsOf(t, err)
	found := findCode(diags, CodeSchema)
	require.NotNil(t, found, "expected a SCHEMA diagnostic, got %+v", diags)
}

func TestParse_EmptySteps(t *testing.T) {
	src := readExample(t, "flow-empty-steps.invalid.yaml")
	_, err := Parse(src)
	require.Error(t, err)
	diags := diagnosticsOf(t, err)
	found := findCode(diags, CodeNoSteps)
	require.NotNil(t, found, "expected a NO_STEPS diagnostic, got %+v", diags)
}

func TestParse_MissingVersion(t *testing.T) {
	_, err := Parse("id: x\nsteps:\n  - id: a\n    call: order-service.createOrder\n")
	require.Error(t, err)
	diags := diagnosticsOf(t, err)
	found := findCode(diags, CodeFlowVersion)
	require.NotNil(t, found, "expected a FLOW_VERSION diagnostic, got %+v", diags)
}

func TestParse_WrongVersion(t *testing.T) {
	_, err := Parse("version: 2\nsteps:\n  - id: a\n    call: order-service.createOrder\n")
	require.Error(t, err)
	diags := diagnosticsOf(t, err)
	found := findCode(diags, CodeFlowVersion)
	require.NotNil(t, found, "expected a FLOW_VERSION diagnostic, got %+v", diags)
}

func TestParse_ValidExample(t *testing.T) {
	src := readExample(t, "flow.valid.yaml")
	f, err := Parse(src)
	require.NoError(t, err)
	assert.Equal(t, "order-allocation", f.ID)
	assert.Len(t, f.Steps, 3)
}

func TestParseFile_DefaultID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "smoke.flow.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\nsteps:\n  - id: a\n    call: order-service.createOrder\n"), 0o644))

	f, err := ParseFile(path)
	require.NoError(t, err)
	assert.Equal(t, "smoke", f.ID)
	assert.Equal(t, path, f.Path)
}

func TestParseFile_ExplicitID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "smoke.flow.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\nid: custom\nsteps:\n  - id: a\n    call: order-service.createOrder\n"), 0o644))

	f, err := ParseFile(path)
	require.NoError(t, err)
	assert.Equal(t, "custom", f.ID)
}

func TestParseFile_NotFound(t *testing.T) {
	_, err := ParseFile(filepath.Join(t.TempDir(), "nope.flow.yaml"))
	require.Error(t, err)
	assert.Equal(t, errs.FlowNotFound, errs.CodeOf(err))
}

func TestIdFromPath(t *testing.T) {
	cases := map[string]string{
		"smoke.flow.yaml":  "smoke",
		"smoke.flow.yml":   "smoke",
		"/a/b/c.flow.yaml": "c",
		"weird-name.yaml":  "weird-name",
		"no-extension":     "no-extension",
	}
	for path, want := range cases {
		assert.Equal(t, want, idFromPath(path), path)
	}
}

func TestDefaultPath(t *testing.T) {
	assert.Equal(t, filepath.Join("dir", "my-flow.flow.yaml"), DefaultPath("dir", "my-flow"))
}

func TestSummary(t *testing.T) {
	f, err := Parse(lineNumberFlow)
	require.NoError(t, err)
	f.Path = "flows/t.flow.yaml"
	f.OwnerKind = "workspace"
	f.Tags = []string{"smoke"}

	s := Summary(f)
	assert.Equal(t, "t", s.ID)
	assert.Equal(t, "flows/t.flow.yaml", s.Path)
	assert.Equal(t, "workspace", s.OwnerKind)
	assert.Equal(t, []string{"smoke"}, s.Tags)
	assert.Equal(t, 2, s.StepCount)
	assert.Equal(t, []string{"order-service.createOrder", "allocation-service.allocate"}, s.Operations)
	assert.NotEmpty(t, s.Hash)
	assert.True(t, s.Updated.IsZero(), "Summary should not stamp Updated -- that's the persistence layer's job")
}

func TestSummary_HashStableAndSensitiveToSource(t *testing.T) {
	f1, err := Parse(lineNumberFlow)
	require.NoError(t, err)
	f2, err := Parse(lineNumberFlow)
	require.NoError(t, err)
	assert.Equal(t, Summary(f1).Hash, Summary(f2).Hash, "same source must hash the same")

	other := `version: 1
id: t2
steps:
  - id: a
    call: order-service.createOrder
`
	f3, err := Parse(other)
	require.NoError(t, err)
	assert.NotEqual(t, Summary(f1).Hash, Summary(f3).Hash)
}

func TestSummary_HashFallsBackWhenSourceEmpty(t *testing.T) {
	f := &domain.Flow{Version: 1, ID: "x", Steps: []domain.Step{{ID: "a", Call: "order-service.createOrder"}}}
	s := Summary(f)
	assert.NotEmpty(t, s.Hash)
}

func TestUses_DistinctInOrder(t *testing.T) {
	f := &domain.Flow{Steps: []domain.Step{
		{ID: "a", Call: "order-service.createOrder"},
		{ID: "b", Call: "allocation-service.allocate"},
		{ID: "c", Call: "order-service.createOrder"},
	}}
	assert.Equal(t, []string{"order-service.createOrder", "allocation-service.allocate"}, Uses(f))
}

func TestUses_Nil(t *testing.T) {
	assert.Nil(t, Uses(nil))
}

func TestSave_NewFileAndOverwriteSameID(t *testing.T) {
	dir := t.TempDir()
	path := DefaultPath(dir, "my-flow")

	src1 := "version: 1\nid: my-flow\nsteps:\n  - id: a\n    call: order-service.createOrder\n"
	require.NoError(t, Save(path, src1))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, src1, string(got))

	src2 := "version: 1\nid: my-flow\nsteps:\n  - id: a\n    call: order-service.createOrder\n  - id: b\n    call: allocation-service.allocate\n"
	require.NoError(t, Save(path, src2))
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, src2, string(got))
}

func TestSave_RefusesOverwriteDifferentID(t *testing.T) {
	dir := t.TempDir()
	path := DefaultPath(dir, "my-flow")
	require.NoError(t, Save(path, "version: 1\nid: my-flow\nsteps:\n  - id: a\n    call: order-service.createOrder\n"))

	err := Save(path, "version: 1\nid: other-flow\nsteps:\n  - id: a\n    call: order-service.createOrder\n")
	require.Error(t, err)
	assert.Equal(t, errs.Conflict, errs.CodeOf(err))
}

func TestSave_CreatesParentDirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "flows")
	path := DefaultPath(dir, "new-flow")
	require.NoError(t, Save(path, "version: 1\nid: new-flow\nsteps:\n  - id: a\n    call: order-service.createOrder\n"))
	_, err := os.Stat(path)
	require.NoError(t, err)
}

// ---- test helpers ---------------------------------------------------------

func readExample(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "spec", "examples", name))
	require.NoError(t, err)
	return string(data)
}

func diagnosticsOf(t *testing.T, err error) []domain.Diagnostic {
	t.Helper()
	e := errs.As(err)
	require.NotNil(t, e)
	diags, ok := e.Details["diagnostics"].([]domain.Diagnostic)
	require.True(t, ok, "error details should carry []domain.Diagnostic, got %#v", e.Details["diagnostics"])
	return diags
}

func findCode(diags []domain.Diagnostic, code string) *domain.Diagnostic {
	for i := range diags {
		if diags[i].Code == code {
			return &diags[i]
		}
	}
	return nil
}
