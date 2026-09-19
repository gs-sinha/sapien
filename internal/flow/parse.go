package flow

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/spec"
)

// flowDoc mirrors domain.Flow for YAML decoding. Steps is []stepDoc (rather
// than []domain.Step) purely so each step's own UnmarshalYAML hook can
// capture its source line; toDomain converts the result to domain.Flow.
type flowDoc struct {
	Version     int                         `yaml:"version"`
	ID          string                      `yaml:"id,omitempty"`
	Name        string                      `yaml:"name,omitempty"`
	Description string                      `yaml:"description,omitempty"`
	Tags        []string                    `yaml:"tags,omitempty"`
	Inputs      map[string]domain.InputSpec `yaml:"inputs,omitempty"`
	Setup       []stepDoc                   `yaml:"setup,omitempty"`
	Steps       []stepDoc                   `yaml:"steps"`
	Teardown    []stepDoc                   `yaml:"teardown,omitempty"`
}

// stepDoc mirrors domain.Step except Assert is []assertDoc (so each
// assertion's own UnmarshalYAML hook can distinguish the bare-CEL-string
// form from the structured-mapping form and capture its source line).
type stepDoc struct {
	ID      string                 `yaml:"id"`
	Call    string                 `yaml:"call"`
	Example string                 `yaml:"example,omitempty"`
	When    string                 `yaml:"when,omitempty"`
	Input   map[string]any         `yaml:"input,omitempty"`
	Params  *domain.ExplicitParams `yaml:"params,omitempty"`
	Body    any                    `yaml:"body,omitempty"`
	Headers map[string]string      `yaml:"headers,omitempty"`
	Extract map[string]string      `yaml:"extract,omitempty"`
	Assert  []assertDoc            `yaml:"assert,omitempty"`
	Until   string                 `yaml:"until,omitempty"`
	Poll    *domain.Poll           `yaml:"poll,omitempty"`
	Timeout string                 `yaml:"timeout,omitempty"`
	Line    int                    `yaml:"-"`
}

// UnmarshalYAML decodes a step mapping node and records its starting line.
func (s *stepDoc) UnmarshalYAML(value *yaml.Node) error {
	type alias stepDoc
	var a alias
	if err := value.Decode(&a); err != nil {
		return err
	}
	*s = stepDoc(a)
	s.Line = value.Line
	return nil
}

// assertDoc wraps a domain.Assertion so it can be decoded from either a bare
// CEL string ("status == 201") or a structured mapping
// ({path: body.x, eq: 1}), recording the source line either way.
type assertDoc struct {
	domain.Assertion
}

// UnmarshalYAML decodes one `assert:` list item, in either its scalar
// (bare CEL) or mapping (structured) form.
func (a *assertDoc) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		a.Assertion = domain.Assertion{Expr: value.Value, Line: value.Line}
		return nil
	}
	// type alias breaks the recursive UnmarshalYAML call: domain.Assertion
	// itself has no custom unmarshaler, so decoding into the alias type
	// falls back to plain field-by-field struct decoding.
	type alias domain.Assertion
	var al alias
	if err := value.Decode(&al); err != nil {
		return err
	}
	result := domain.Assertion(al)
	result.Line = value.Line
	a.Assertion = result
	return nil
}

// toDomain converts a decoded flowDoc into the public domain.Flow shape.
func toDomain(fd flowDoc) *domain.Flow {
	f := &domain.Flow{
		Version:     fd.Version,
		ID:          fd.ID,
		Name:        fd.Name,
		Description: fd.Description,
		Tags:        fd.Tags,
		Inputs:      fd.Inputs,
	}
	f.Setup = stepsToDomain(fd.Setup)
	f.Steps = stepsToDomain(fd.Steps)
	f.Teardown = stepsToDomain(fd.Teardown)
	return f
}

// stepsToDomain converts a decoded []stepDoc (Setup, Steps, or Teardown) into
// []domain.Step. Returns nil for an empty/nil input so an omitted `setup:`
// or `teardown:` round-trips as the zero value rather than an empty slice.
func stepsToDomain(docs []stepDoc) []domain.Step {
	if len(docs) == 0 {
		return nil
	}
	out := make([]domain.Step, len(docs))
	for i, sd := range docs {
		st := domain.Step{
			ID:      sd.ID,
			Call:    sd.Call,
			Example: sd.Example,
			When:    sd.When,
			Input:   sd.Input,
			Params:  sd.Params,
			Body:    sd.Body,
			Headers: sd.Headers,
			Extract: sd.Extract,
			Until:   sd.Until,
			Poll:    sd.Poll,
			Timeout: sd.Timeout,
			Line:    sd.Line,
		}
		if len(sd.Assert) > 0 {
			st.Assert = make([]domain.Assertion, len(sd.Assert))
			for j, ad := range sd.Assert {
				st.Assert[j] = ad.Assertion
			}
		}
		out[i] = st
	}
	return out
}

// Parse parses and validates src (the contents of a *.flow.yaml file)
// against the flow JSON Schema, returning a domain.Flow with Steps[i].Line
// and Assert[j].Line populated from the source.
//
// On any problem -- malformed YAML or a schema violation -- Parse returns a
// nil *domain.Flow and an *errs.Error with Code errs.FlowInvalid whose
// Details["diagnostics"] is a []domain.Diagnostic (see problemsToDiagnostics
// for how spec.Problem is translated into diagnostic codes).
func Parse(src string) (*domain.Flow, error) {
	// Checked before schema validation: yaml.v3 itself rejects ANY literal
	// duplicate mapping key while decoding into a generic value, which is
	// exactly what spec.ValidateYAML's first pass does. Left to that, a
	// duplicate `extract:` name would surface as an opaque "invalid YAML"
	// SCHEMA problem with no indication it was specifically an extract
	// name; catching it here first (via a yaml.Node walk, which tolerates
	// duplicate keys) gives the precise DUPLICATE_EXTRACT diagnostic
	// instead.
	if diags := findDuplicateExtracts(&domain.Flow{Source: src}); len(diags) > 0 {
		return nil, flowInvalidErr(diags)
	}

	problems := spec.ValidateYAML(spec.Flow, []byte(src))
	if len(problems) > 0 {
		return nil, flowInvalidErr(problemsToDiagnostics(problems))
	}

	var fd flowDoc
	if err := yaml.Unmarshal([]byte(src), &fd); err != nil {
		// The schema pass above should have already caught anything that
		// would fail here; kept as a defensive fallback.
		diag := domain.Diagnostic{
			Code:     CodeSchema,
			Severity: domain.SeverityError,
			Message:  "invalid YAML: " + err.Error(),
		}
		return nil, flowInvalidErr([]domain.Diagnostic{diag})
	}

	f := toDomain(fd)
	f.Source = src
	return f, nil
}

func flowInvalidErr(diags []domain.Diagnostic) error {
	return errs.New(errs.FlowInvalid, "flow validation failed: %d problem(s)", len(diags)).
		WithDetail("diagnostics", diags)
}

// ParseFile reads and parses the flow file at path, then sets Path and
// (when the file itself has no `id:`) ID to the file's stem with a trailing
// ".flow.yaml"/".flow.yml" removed (see DefaultPath).
func ParseFile(path string) (*domain.Flow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.Wrap(errs.FlowNotFound, err, "reading flow file %s", path)
	}
	f, err := Parse(string(data))
	if err != nil {
		return nil, err
	}
	f.Path = path
	if f.ID == "" {
		f.ID = idFromPath(path)
	}
	return f, nil
}

// idFromPath derives the default flow ID from a file path: the base name
// with a ".flow.yaml"/".flow.yml" suffix removed, falling back to stripping
// a plain extension for any other name.
func idFromPath(path string) string {
	base := filepath.Base(path)
	for _, suf := range []string{".flow.yaml", ".flow.yml"} {
		if strings.HasSuffix(base, suf) {
			return strings.TrimSuffix(base, suf)
		}
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// DefaultPath returns the conventional file path for flow id under dir:
// <dir>/<id>.flow.yaml.
func DefaultPath(dir, id string) string {
	return filepath.Join(dir, id+".flow.yaml")
}

// Summary reduces f to its catalog view. Operations is the distinct set of
// step calls in the order they first appear (see Uses); Hash is the SHA-256
// of f.Source, or of f re-marshalled to YAML when Source is empty (e.g. a
// flow built programmatically rather than via Parse). Updated is left zero:
// it is the persistence layer's concern, not this package's.
func Summary(f *domain.Flow) domain.FlowSummary {
	return domain.FlowSummary{
		ID:         f.ID,
		Name:       f.Name,
		Path:       f.Path,
		OwnerKind:  f.OwnerKind,
		OwnerID:    f.OwnerID,
		Tags:       f.Tags,
		Operations: Uses(f),
		StepCount:  len(f.Steps),
		Hash:       flowHash(f),
	}
}

func flowHash(f *domain.Flow) string {
	data := []byte(f.Source)
	if len(data) == 0 {
		if b, err := yaml.Marshal(f); err == nil {
			data = b
		} else {
			data = []byte(f.ID)
		}
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Uses returns the distinct operation IDs f's setup, main, and teardown
// steps call, in the order they first appear (setup, then steps, then
// teardown).
func Uses(f *domain.Flow) []string {
	if f == nil {
		return nil
	}
	total := len(f.Setup) + len(f.Steps) + len(f.Teardown)
	seen := make(map[string]bool, total)
	out := make([]string, 0, total)
	for _, st := range AllSteps(f) {
		if st.Call == "" || seen[st.Call] {
			continue
		}
		seen[st.Call] = true
		out = append(out, st.Call)
	}
	return out
}

// flowIDOf returns the effective flow id for src (the raw YAML for path):
// its own `id:` field if set, otherwise DefaultPath's inverse -- the ID that
// idFromPath(path) would produce. It does not require src to be a
// schema-valid flow; it is only used by Save's overwrite guard.
func flowIDOf(path, src string) string {
	var partial struct {
		ID string `yaml:"id"`
	}
	_ = yaml.Unmarshal([]byte(src), &partial) // best effort; zero value on failure
	if partial.ID != "" {
		return partial.ID
	}
	return idFromPath(path)
}

// Save atomically writes src to path, creating parent directories as
// needed. If path already exists, Save refuses to overwrite it unless the
// existing file's flow id (its own `id:`, or the ID implied by its file
// name) matches src's -- so re-saving edits to a flow a caller already owns
// always succeeds, but a caller can't accidentally clobber an unrelated
// file. Use DefaultPath for a brand-new flow's path to avoid the check
// entirely (nothing exists there yet).
func Save(path string, src string) error {
	if existing, err := os.ReadFile(path); err == nil {
		oldID := flowIDOf(path, string(existing))
		newID := flowIDOf(path, src)
		if oldID != newID {
			return errs.New(errs.Conflict,
				"refusing to overwrite %s: existing flow id `%s` does not match `%s`", path, oldID, newID)
		}
	} else if !os.IsNotExist(err) {
		return errs.Wrap(errs.Internal, err, "checking existing file %s", path)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errs.Wrap(errs.Internal, err, "creating directory for %s", path)
	}
	return atomicWrite(dir, path, []byte(src))
}

func atomicWrite(dir, path string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".flow-*.tmp")
	if err != nil {
		return errs.Wrap(errs.Internal, err, "creating temp file for %s", path)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return errs.Wrap(errs.Internal, err, "writing %s", path)
	}
	if err := tmp.Close(); err != nil {
		return errs.Wrap(errs.Internal, err, "closing temp file for %s", path)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return errs.Wrap(errs.Internal, err, "setting permissions for %s", path)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return errs.Wrap(errs.Internal, err, "renaming into place for %s", path)
	}
	return nil
}
