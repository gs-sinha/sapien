package example

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// idPattern matches valid example IDs: lowercase letters, digits, '-', '_',
// and '.'.
var idPattern = regexp.MustCompile(`^[a-z0-9._-]+$`)

// operationPattern matches operation IDs shaped "<service>.<operationId>":
// a non-empty prefix, a literal dot, then a non-empty remainder (which may
// itself contain further dots, e.g. "order-service.orders.create").
var operationPattern = regexp.MustCompile(`^[^.]+\.[^.]+`)

// Parse parses data (the full YAML content of a "<id>.example.yaml" file)
// into a domain.SavedExample. path is used to default ID from the file's
// stem when the YAML omits it, and is recorded on the result's Path field.
// Service is derived from Operation ("<service>.<operationId>" -> the
// prefix before the first dot); Scope is not set by Parse (it depends on
// which directory the file was found under, which Parse does not know) and
// must be set by the caller.
func Parse(data []byte, path string) (*domain.SavedExample, error) {
	var ex domain.SavedExample
	if err := yaml.Unmarshal(data, &ex); err != nil {
		return nil, errs.Wrap(errs.Invalid, err, "parse example file %s", path)
	}
	if ex.ID == "" {
		ex.ID = idFromPath(path)
	}
	ex.Service = serviceFromOperation(ex.Operation)
	ex.Path = path
	return &ex, nil
}

// Marshal renders ex as the full "<id>.example.yaml" file content. It
// encodes ex directly: every domain.SavedExample field already carries the
// yaml tag PLAN.md §34b's example shows (Scope/Service/Path are tagged
// "-" and so are omitted), and empty fields are omitted via "omitempty".
// Timestamps are RFC3339 (time.Time's default yaml.v3 encoding).
func Marshal(ex *domain.SavedExample) ([]byte, error) {
	cp := *ex
	cp.Input, _ = normalizeNumbers(cp.Input).(map[string]any)
	cp.Body = normalizeNumbers(cp.Body)
	if cp.Expect != nil {
		e := *cp.Expect
		e.Body = normalizeNumbers(e.Body)
		cp.Expect = &e
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&cp); err != nil {
		_ = enc.Close()
		return nil, errs.Wrap(errs.Internal, err, "marshal example %s", ex.ID)
	}
	if err := enc.Close(); err != nil {
		return nil, errs.Wrap(errs.Internal, err, "marshal example %s", ex.ID)
	}
	return buf.Bytes(), nil
}

// writeFile atomically writes Marshal(ex) to ex.Path (create parent
// directories as needed, write to a temp file in the same directory, then
// rename over the destination). Mirrors memory.WriteFile.
func writeFile(ex *domain.SavedExample) error {
	if ex.Path == "" {
		return errs.New(errs.Invalid, "example has no file path to write to")
	}
	dir := filepath.Dir(ex.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errs.Wrap(errs.Internal, err, "create example directory %s", dir)
	}

	data, err := Marshal(ex)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".example-*.tmp")
	if err != nil {
		return errs.Wrap(errs.Internal, err, "create temp file in %s", dir)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return errs.Wrap(errs.Internal, err, "write example temp file")
	}
	if err := tmp.Close(); err != nil {
		return errs.Wrap(errs.Internal, err, "close example temp file")
	}
	if err := os.Rename(tmpPath, ex.Path); err != nil {
		return errs.Wrap(errs.Internal, err, "rename example temp file to %s", ex.Path)
	}
	return nil
}

// readFile reads and parses the example file at path, setting Scope on the
// result (Parse cannot; it does not know which directory path came from).
func readFile(path string, scope domain.ExampleScope) (*domain.SavedExample, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "read example file %s", path)
	}
	ex, err := Parse(data, path)
	if err != nil {
		return nil, err
	}
	ex.Scope = scope
	return ex, nil
}

// idFromPath returns the file stem (no directory, no ".example.yaml"
// suffix) of path, used as an example's default ID when its YAML omits one.
func idFromPath(path string) string {
	return strings.TrimSuffix(filepath.Base(path), exampleFileSuffix)
}

// serviceFromOperation extracts the service name from an operation ID of
// the form "<service-name>.<operationId>" (PLAN §5). Returns "" if opID has
// no service prefix.
func serviceFromOperation(opID string) string {
	if i := strings.Index(opID, "."); i > 0 {
		return opID[:i]
	}
	return ""
}

// validateID checks that id is non-empty and matches idPattern.
func validateID(id string) error {
	if id == "" {
		return errs.New(errs.Invalid, "example: id is required")
	}
	if !idPattern.MatchString(id) {
		return errs.New(errs.Invalid, "example: id %q must contain only lowercase letters, digits, '-', '_', or '.'", id).WithDetail("id", id)
	}
	return nil
}

// validateOperation checks that operation is non-empty and shaped
// "<service>.<operationId>".
func validateOperation(operation string) error {
	if operation == "" {
		return errs.New(errs.Invalid, "example: operation is required")
	}
	if !operationPattern.MatchString(operation) {
		return errs.New(errs.Invalid, `example: operation %q must be "<service>.<operationId>" shaped`, operation).WithDetail("operation", operation)
	}
	return nil
}

// normalizeNumbers returns v with every integral float64 (what encoding/json
// produces for any JSON number, including epoch-millisecond timestamps such
// as 1788589814724) replaced by an int64, recursively through maps and
// slices. Without it yaml.v3 writes such values as 1.788589814724e+12,
// which is lossy past 15 significant digits and reads as a mistake.
// Non-integral floats and everything else pass through unchanged.
func normalizeNumbers(v any) any {
	switch x := v.(type) {
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return int64(x)
		}
		return x
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = normalizeNumbers(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = normalizeNumbers(val)
		}
		return out
	default:
		return v
	}
}
