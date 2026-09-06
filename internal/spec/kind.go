// Package spec compiles and validates the JSON Schemas (draft 2020-12) that
// describe Sapien's YAML file formats: sapien.workspace.yaml, service.yaml,
// environments/<name>.yaml, *.flow.yaml, and the YAML front matter of
// memories/*.md. The canonical schema sources live in the repository's
// spec/ directory; this package embeds a synchronized copy so it needs no
// filesystem access at runtime (see schemas_sync_test.go).
package spec

// Kind identifies which Sapien YAML document shape a schema validates.
type Kind string

// The document kinds Sapien validates. See PLAN.md sections 6, 7, 8, 10, 11,
// 34b and internal/domain/{workspace,service,flow,memory,environment,example}.go.
const (
	Workspace   Kind = "workspace"
	Service     Kind = "service"
	Environment Kind = "environment"
	Flow        Kind = "flow"
	Memory      Kind = "memory"
	Example     Kind = "example"
)

// allKinds is the closed set of valid Kind values, in a stable order.
var allKinds = []Kind{Workspace, Service, Environment, Flow, Memory, Example}

// fileName returns the embedded schema file name for k, e.g. "flow.schema.json".
func (k Kind) fileName() string {
	return string(k) + ".schema.json"
}

// String implements fmt.Stringer.
func (k Kind) String() string {
	return string(k)
}
