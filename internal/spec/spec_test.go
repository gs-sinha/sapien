package spec_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gs-sinha/sapien/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const examplesDir = "../../spec/examples"

var allKinds = []spec.Kind{spec.Workspace, spec.Service, spec.Environment, spec.Flow, spec.Memory, spec.Example}

// kindForExampleFile maps an example file name to the schema Kind it is
// meant to validate against, based on its "<kind>.valid.yaml" or
// "<kind>-<case>.invalid.yaml" naming convention.
func kindForExampleFile(name string) (spec.Kind, bool) {
	for _, k := range allKinds {
		prefix := string(k)
		if strings.HasPrefix(name, prefix+".") || strings.HasPrefix(name, prefix+"-") {
			return k, true
		}
	}
	return "", false
}

// TestExamples walks spec/examples and checks that every "*.valid.yaml"
// validates with zero problems and every "*.invalid.yaml" produces at least
// one, against the schema its file name prefix selects.
func TestExamples(t *testing.T) {
	entries, err := os.ReadDir(examplesDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "expected example files under %s", examplesDir)

	seenValid := map[spec.Kind]bool{}
	seenInvalidCount := map[spec.Kind]int{}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			k, ok := kindForExampleFile(name)
			require.Truef(t, ok, "no schema Kind mapping for example file %q", name)

			src, err := os.ReadFile(filepath.Join(examplesDir, name))
			require.NoError(t, err)

			problems := spec.ValidateYAML(k, src)

			switch {
			case strings.HasSuffix(name, ".invalid.yaml"):
				seenInvalidCount[k]++
				assert.NotEmptyf(t, problems, "expected %s to fail validation, but it validated cleanly", name)

				firstLine := strings.TrimSpace(strings.SplitN(string(src), "\n", 2)[0])
				assert.Truef(t, strings.HasPrefix(firstLine, "#"),
					"%s: first line must be a '#' comment explaining why the file is invalid, got %q", name, firstLine)

			case strings.HasSuffix(name, ".valid.yaml"):
				seenValid[k] = true
				assert.Emptyf(t, problems, "expected %s to validate cleanly, got: %v", name, problems)

			default:
				t.Fatalf("example file %q must end in .valid.yaml or .invalid.yaml", name)
			}
		})
	}

	for _, k := range allKinds {
		assert.Truef(t, seenValid[k], "expected at least one <%s>.valid.yaml example", k)
		assert.GreaterOrEqualf(t, seenInvalidCount[k], 2, "expected at least two invalid examples for %s", k)
	}
}

// TestValidateYAML_LineNumbers checks that a nested validation failure gets
// mapped back to a non-zero source line by walking the yaml.Node tree.
func TestValidateYAML_LineNumbers(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(examplesDir, "flow-bad-call-pattern.invalid.yaml"))
	require.NoError(t, err)

	problems := spec.ValidateYAML(spec.Flow, src)
	require.NotEmpty(t, problems)

	var nested *spec.Problem
	for i := range problems {
		if problems[i].Path != "" {
			nested = &problems[i]
			break
		}
	}
	require.NotNilf(t, nested, "expected at least one problem with a non-root path: %+v", problems)
	assert.Greaterf(t, nested.Line, 0, "expected a resolved (non-zero) line number for %+v", nested)
}

// TestValidateYAML_UnknownKeyRejection exercises the two cases the task
// calls out explicitly: an unknown key on a flow step (a reserved future
// key, "when") and an unknown top-level workspace key.
func TestValidateYAML_UnknownKeyRejection(t *testing.T) {
	t.Run("flow step when", func(t *testing.T) {
		src := []byte("version: 1\nid: f\nsteps:\n  - id: a\n    call: svc.op\n    when: \"${true}\"\n")
		problems := spec.ValidateYAML(spec.Flow, src)
		require.NotEmpty(t, problems)
		assert.True(t, anyMessageContains(problems, `"when"`), "expected a problem naming the unknown \"when\" key: %+v", problems)
	})

	t.Run("workspace unknown key", func(t *testing.T) {
		src := []byte("version: 1\nname: ws\nrepos: []\n")
		problems := spec.ValidateYAML(spec.Workspace, src)
		require.NotEmpty(t, problems)
		assert.True(t, anyMessageContains(problems, `"repos"`), "expected a problem naming the unknown \"repos\" key: %+v", problems)
	})
}

func anyMessageContains(problems []spec.Problem, substr string) bool {
	for _, p := range problems {
		if strings.Contains(p.Message, substr) {
			return true
		}
	}
	return false
}

// TestValidate_DirectDoc exercises the Validate(kind, doc any) entry point
// directly (no YAML source), which must always report Line == 0.
func TestValidate_DirectDoc(t *testing.T) {
	valid := map[string]any{"version": 1, "name": "logistics"}
	assert.Empty(t, spec.Validate(spec.Workspace, valid))

	invalid := map[string]any{"version": 1, "name": "logistics", "repos": []any{}}
	problems := spec.Validate(spec.Workspace, invalid)
	require.NotEmpty(t, problems)
	for _, p := range problems {
		assert.Equal(t, 0, p.Line, "Validate has no source text, so Line must stay 0")
	}
}

// TestValidate_UnknownKind checks the error path for a Kind value that
// isn't one of the package's constants.
func TestValidate_UnknownKind(t *testing.T) {
	problems := spec.Validate(spec.Kind("bogus"), map[string]any{})
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0].Message, "unknown kind")
}

// TestSchema checks the raw-schema accessor used to serve, e.g.,
// flow.schema.json to MCP hosts.
func TestSchema(t *testing.T) {
	for _, k := range allKinds {
		k := k
		t.Run(string(k), func(t *testing.T) {
			b := spec.Schema(k)
			assert.NotEmpty(t, b)
			var doc map[string]any
			require.NoError(t, json.Unmarshal(b, &doc), "schema for %s must be valid JSON", k)
			assert.Equal(t, "https://json-schema.org/draft/2020-12/schema", doc["$schema"])
		})
	}
	assert.Panics(t, func() { spec.Schema(spec.Kind("nope")) })
}

// TestValidate_RawTimeTimeIsInvalidJSONValue documents (and exercises) why
// ValidateYAML must normalize before validating: gopkg.in/yaml.v3 decodes an
// unquoted timestamp-looking scalar into time.Time (not string) when the
// unmarshal target is `any`, and jsonschema/v6 has no notion of a Go
// time.Time -- calling Validate directly with one produces an opaque
// "unsupported value type time.Time" problem instead of the intended
// format/date-time diagnostic.
func TestValidate_RawTimeTimeIsInvalidJSONValue(t *testing.T) {
	doc := map[string]any{
		"id":      "mem_01J8Z5K3W2RQ4X7M9NPQR2T3V8",
		"scope":   "workspace",
		"created": time.Now(),
	}
	problems := spec.Validate(spec.Memory, doc)
	require.NotEmpty(t, problems)
	assert.True(t, anyMessageContains(problems, "unsupported value type"), "expected an 'unsupported value type' problem: %+v", problems)
}

// TestDescribeKindCoverage exercises a handful of schema constraints that
// none of the spec/examples files happen to trigger, so the corresponding
// jsonschema/v6 kind.* -> message translations in messages.go are covered.
func TestDescribeKindCoverage(t *testing.T) {
	cases := []struct {
		name string
		kind spec.Kind
		yaml string
		want string
	}{
		{
			name: "minimum (negative timeout_ms)",
			kind: spec.Environment,
			yaml: "version: 1\nname: staging\ntransport:\n  timeout_ms: -5\n",
			want: "must be >=",
		},
		{
			name: "format (bad date-time)",
			kind: spec.Memory,
			yaml: "id: mem_01J8Z5K3W2RQ4X7M9NPQR2T3V8\nscope: workspace\ncreated: not-a-date\n",
			want: "invalid date-time",
		},
		{
			name: "minLength (empty name)",
			kind: spec.Workspace,
			yaml: "version: 1\nname: \"\"\n",
			want: "length must be >=",
		},
		{
			name: "minProperties (empty structured assertion)",
			kind: spec.Flow,
			yaml: "version: 1\nsteps:\n  - id: a\n    call: svc.op\n    assert:\n      - {}\n",
			want: "must have at least",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			problems := spec.ValidateYAML(tc.kind, []byte(tc.yaml))
			require.NotEmpty(t, problems)
			assert.True(t, anyMessageContains(problems, tc.want), "expected a problem containing %q, got: %+v", tc.want, problems)
		})
	}
}

// TestValidateYAML_ParseError checks the invalid-YAML-syntax path.
func TestValidateYAML_ParseError(t *testing.T) {
	problems := spec.ValidateYAML(spec.Workspace, []byte("version: [1\n"))
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0].Message, "invalid YAML")
	assert.Equal(t, 0, problems[0].Line)
}

// TestProblemString checks the small String() convenience method.
func TestProblemString(t *testing.T) {
	assert.Equal(t, "boom", spec.Problem{Message: "boom"}.String())
	assert.Equal(t, "steps[0].call: boom", spec.Problem{Path: "steps[0].call", Message: "boom"}.String())
}
