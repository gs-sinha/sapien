package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// TestExamplePathText checks both branches: a reported path passes through,
// and an unreported one (the fake engine never sets SavedExample.Path)
// falls back to an honest placeholder instead of a fabricated file path.
func TestExamplePathText(t *testing.T) {
	assert.Equal(t, "(path not reported)", examplePathText(""))
	assert.Equal(t, "/workspace/examples/foo.example.yaml", examplePathText("/workspace/examples/foo.example.yaml"))
}

// --- merge helpers (execute_api's override precedence) -------------------

// TestMergeExampleParams checks the precedence execute_api's example input
// documents: the example's saved input is the base, and the call's own
// params override it field by field, without disturbing fields the call
// never mentioned.
func TestMergeExampleParams(t *testing.T) {
	cases := []struct {
		name     string
		base     map[string]any
		override map[string]any
		want     map[string]any
	}{
		{
			name:     "override wins on a shared key, base keys not mentioned survive",
			base:     map[string]any{"riderId": "r1", "region": "blr"},
			override: map[string]any{"riderId": "r2"},
			want:     map[string]any{"riderId": "r2", "region": "blr"},
		},
		{name: "no base: override passes through unchanged", base: nil, override: map[string]any{"riderId": "r2"}, want: map[string]any{"riderId": "r2"}},
		{name: "no override: base passes through unchanged", base: map[string]any{"riderId": "r1"}, override: nil, want: map[string]any{"riderId": "r1"}},
		{name: "neither: nil", base: nil, override: nil, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeExampleParams(tc.base, tc.override)
			if tc.want == nil {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

// TestMergeExampleHeaders mirrors TestMergeExampleParams for headers.
func TestMergeExampleHeaders(t *testing.T) {
	base := map[string]string{"X-Trace": "abc", "X-Region": "blr"}
	override := map[string]string{"X-Trace": "override"}
	got := mergeExampleHeaders(base, override)
	assert.Equal(t, map[string]string{"X-Trace": "override", "X-Region": "blr"}, got)

	assert.Empty(t, mergeExampleHeaders(nil, nil))
	assert.Equal(t, override, mergeExampleHeaders(nil, override))
	assert.Equal(t, base, mergeExampleHeaders(base, nil))
}

// --- list_examples -----------------------------------------------------

func TestTool_ListExamples(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "list_examples", map[string]any{})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[ListExamplesOutput](t, res.StructuredContent)
	require.Len(t, out.Examples, 2)

	byID := map[string]ExampleListItem{}
	for _, ex := range out.Examples {
		byID[ex.ID] = ex
	}
	require.Contains(t, byID, "rider-get-example")
	assert.True(t, byID["rider-get-example"].Verified)
	assert.Equal(t, "staging", byID["rider-get-example"].Env)
	require.Contains(t, byID, "rider-create-draft")
	assert.False(t, byID["rider-create-draft"].Verified)
	assert.Contains(t, firstText(res), "rider-get-example")
}

func TestTool_ListExamples_Filters(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")

	res := callTool(t, cs, "list_examples", map[string]any{"operation": "rider-service.getRider"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[ListExamplesOutput](t, res.StructuredContent)
	require.Len(t, out.Examples, 1)
	assert.Equal(t, "rider-get-example", out.Examples[0].ID)

	res = callTool(t, cs, "list_examples", map[string]any{"tag": "draft"})
	require.False(t, res.IsError, firstText(res))
	out = decodeStructured[ListExamplesOutput](t, res.StructuredContent)
	require.Len(t, out.Examples, 1)
	assert.Equal(t, "rider-create-draft", out.Examples[0].ID)

	res = callTool(t, cs, "list_examples", map[string]any{"text": "nope-does-not-match-anything"})
	require.False(t, res.IsError, firstText(res))
	out = decodeStructured[ListExamplesOutput](t, res.StructuredContent)
	assert.Empty(t, out.Examples)
	assert.Contains(t, firstText(res), "no matches")
}

func TestTool_ListExamples_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.ReadContracts = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "list_examples", map[string]any{})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "read_contracts")
}

// --- get_example -----------------------------------------------------

func TestTool_GetExample(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_example", map[string]any{"id": "rider-get-example"})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[GetExampleOutput](t, res.StructuredContent)
	assert.Equal(t, "rider-service.getRider", out.Example.Operation)
	assert.True(t, out.Example.Verified != nil)
	assert.Contains(t, out.YAML, "operation: rider-service.getRider")
	assert.Contains(t, firstText(res), "operation: rider-service.getRider")
}

func TestTool_GetExample_NotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "get_example", map[string]any{"id": "nope"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_EXAMPLE_NOT_FOUND")
}

// --- create_example -----------------------------------------------------

func TestTool_CreateExample_HandWritten(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_example", map[string]any{
		"id": "new-hand-written", "operation": "rider-service.createRider",
		"body": map[string]any{"name": "Zara"}, "description": "another draft", "tags": []any{"draft"},
	})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[CreateExampleOutput](t, res.StructuredContent)
	assert.Equal(t, "new-hand-written", out.Example.ID)
	assert.Equal(t, "rider-service.createRider", out.Example.Operation)
	assert.Nil(t, out.Example.Verified, "a hand-written example must never be verified")
	assert.Contains(t, firstText(res), "unverified")
	assert.Contains(t, firstText(res), "execute_api(example=")
	assert.Contains(t, firstText(res), "example: new-hand-written")
}

func TestTool_CreateExample_FromRun(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")

	ran := callTool(t, cs, "execute_api", map[string]any{
		"id": "rider-service.getRider", "env": "staging", "params": map[string]any{"riderId": "r1"},
	})
	require.False(t, ran.IsError, firstText(ran))
	runID := decodeStructured[RunViewWithHints](t, ran.StructuredContent).RunView.ID
	require.NotEmpty(t, runID)

	res := callTool(t, cs, "create_example", map[string]any{
		"id": "from-run-1", "run_id": runID, "description": "saved from a real call",
	})
	require.False(t, res.IsError, firstText(res))

	out := decodeStructured[CreateExampleOutput](t, res.StructuredContent)
	assert.Equal(t, "rider-service.getRider", out.Example.Operation)
	require.NotNil(t, out.Example.Verified, "a run_id-derived example must be verified")
	assert.Equal(t, runID, out.Example.Verified.RunID)
	assert.Equal(t, "staging", out.Example.Verified.Env)
	require.NotNil(t, out.Example.Verified.Source)
	assert.Equal(t, "agent", out.Example.Verified.Source.Kind)
	assert.Equal(t, "claude-code", out.Example.Verified.Source.Client)
	assert.Contains(t, firstText(res), "verified against staging")
}

func TestTool_CreateExample_InvalidCombinations(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")

	cases := []struct {
		name string
		args map[string]any
	}{
		{"neither run_id nor operation", map[string]any{"id": "x1"}},
		{"both run_id and operation", map[string]any{"id": "x2", "run_id": "run_1", "operation": "rider-service.getRider"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := callTool(t, cs, "create_example", tc.args)
			require.True(t, res.IsError)
			assert.Contains(t, firstText(res), "E_INVALID")
		})
	}
}

// TestTool_CreateExample_MissingID checks that id (required by the tool's
// own schema, so the SDK rejects the call before create_example's handler
// even runs) cannot be omitted -- the id-required check in the handler
// itself (for any caller that skips schema validation) is exercised by this
// still failing closed either way.
func TestTool_CreateExample_MissingID(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "create_example", map[string]any{"operation": "rider-service.getRider"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "id")
}

func TestTool_CreateExample_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.WriteExamples = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "create_example", map[string]any{"id": "x", "operation": "rider-service.getRider"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "write_examples")
}

// --- rescope_example -----------------------------------------------------

func TestTool_RescopeExample(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "rescope_example", map[string]any{"id": "rider-get-example", "scope": "service"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[RescopeExampleOutput](t, res.StructuredContent)
	assert.Equal(t, domain.ExampleScope("service"), out.Example.Scope)
	assert.Contains(t, firstText(res), "rescoped example rider-get-example to service scope")
}

func TestTool_RescopeExample_NotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "rescope_example", map[string]any{"id": "nope", "scope": "service"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_EXAMPLE_NOT_FOUND")
}

func TestTool_RescopeExample_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.WriteExamples = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "rescope_example", map[string]any{"id": "rider-get-example", "scope": "service"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "write_examples")
}

// --- delete_example -----------------------------------------------------

func TestTool_DeleteExample(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "delete_example", map[string]any{"id": "rider-create-draft"})
	require.False(t, res.IsError, firstText(res))
	out := decodeStructured[DeleteExampleOutput](t, res.StructuredContent)
	assert.Equal(t, "rider-create-draft", out.ID)

	again := callTool(t, cs, "get_example", map[string]any{"id": "rider-create-draft"})
	assert.True(t, again.IsError)
}

func TestTool_DeleteExample_NotFound(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "delete_example", map[string]any{"id": "nope"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_EXAMPLE_NOT_FOUND")
}

func TestTool_DeleteExample_PermissionDenied(t *testing.T) {
	p := DefaultPermissions()
	p.WriteExamples = false
	cs := newTestSession(t, Config{Default: p}, "claude-code")
	res := callTool(t, cs, "delete_example", map[string]any{"id": "rider-get-example"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "write_examples")
}

// --- execute_api's `example` input --------------------------------------

// TestTool_ExecuteAPI_ExampleResolvesOperationWithoutID checks that id may
// be omitted when example is given, and that the permission class used is
// the one for the *example's* operation (createRider is a POST, so
// execute_mutation -- denied by DefaultPermissions -- is what must be
// reported, proving the operation really was resolved from the example
// rather than defaulting to something else).
func TestTool_ExecuteAPI_ExampleResolvesOperationWithoutID(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "execute_api", map[string]any{"example": "rider-create-draft", "env": "staging"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "execute_mutation")
}

// TestTool_ExecuteAPI_ExampleSucceedsAndMentionsIt checks the success path
// with a GET example (only execute_read needed, granted by default): the
// text names the example, and the run comes back normally.
func TestTool_ExecuteAPI_ExampleSucceedsAndMentionsIt(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "execute_api", map[string]any{"example": "rider-get-example", "env": "staging"})
	require.False(t, res.IsError, firstText(res))
	assert.Contains(t, firstText(res), "from example rider-get-example")

	out := decodeStructured[RunViewWithHints](t, res.StructuredContent)
	assert.Equal(t, "rider-service.getRider", out.RunView.Steps[0].Operation)
}

// TestTool_ExecuteAPI_ExampleAndIDMustAgree checks both directions: a
// matching id is fine, a mismatched one is E_INVALID.
func TestTool_ExecuteAPI_ExampleAndIDMustAgree(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")

	ok := callTool(t, cs, "execute_api", map[string]any{
		"example": "rider-get-example", "id": "rider-service.getRider", "env": "staging",
	})
	assert.False(t, ok.IsError, firstText(ok))

	mismatch := callTool(t, cs, "execute_api", map[string]any{
		"example": "rider-get-example", "id": "rider-service.createRider", "env": "staging",
	})
	require.True(t, mismatch.IsError)
	assert.Contains(t, firstText(mismatch), "E_INVALID")
}

// TestTool_ExecuteAPI_NeitherIDNorExample checks the new "at least one of
// id/example" requirement now that id alone is no longer mandatory.
func TestTool_ExecuteAPI_NeitherIDNorExample(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "execute_api", map[string]any{"env": "staging"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_INVALID")
}

// TestTool_ExecuteAPI_UnknownExample checks that a bad example id surfaces
// the example store's own not-found error rather than a generic failure.
func TestTool_ExecuteAPI_UnknownExample(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")
	res := callTool(t, cs, "execute_api", map[string]any{"example": "nope", "env": "staging"})
	require.True(t, res.IsError)
	assert.Contains(t, firstText(res), "E_EXAMPLE_NOT_FOUND")
}

// --- get_api's saved-examples list ---------------------------------------

// TestTool_GetAPI_SavedExamplesList checks that get_api surfaces the saved
// examples of an operation at detail=fields and detail=full (but not
// detail=summary), each carrying id, verified, and description.
func TestTool_GetAPI_SavedExamplesList(t *testing.T) {
	cs := newTestSession(t, Config{Default: DefaultPermissions()}, "claude-code")

	summary := callTool(t, cs, "get_api", map[string]any{"id": "rider-service.getRider"})
	require.False(t, summary.IsError, firstText(summary))
	summaryOut := decodeStructured[GetAPIOutput](t, summary.StructuredContent)
	assert.Empty(t, summaryOut.Examples, "summary detail must not include saved examples")

	fields := callTool(t, cs, "get_api", map[string]any{"id": "rider-service.getRider", "detail": "fields"})
	require.False(t, fields.IsError, firstText(fields))
	fieldsOut := decodeStructured[GetAPIOutput](t, fields.StructuredContent)
	require.Len(t, fieldsOut.Examples, 1)
	assert.Equal(t, "rider-get-example", fieldsOut.Examples[0].ID)
	assert.True(t, fieldsOut.Examples[0].Verified)
	assert.Contains(t, firstText(fields), "rider-get-example (verified)")

	full := callTool(t, cs, "get_api", map[string]any{"id": "rider-service.createRider", "detail": "full"})
	require.False(t, full.IsError, firstText(full))
	fullOut := decodeStructured[GetAPIOutput](t, full.StructuredContent)
	require.Len(t, fullOut.Examples, 1)
	assert.Equal(t, "rider-create-draft", fullOut.Examples[0].ID)
	assert.False(t, fullOut.Examples[0].Verified)
	assert.Contains(t, firstText(full), "rider-create-draft (unverified)")
}
