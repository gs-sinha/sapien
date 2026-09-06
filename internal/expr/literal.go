package expr

import (
	"encoding/json"

	"github.com/gs-sinha/sapien/internal/errs"
)

// celLiteral renders a Go value as CEL literal source text. JSON's literal
// grammar for strings/numbers/bools/null/objects/arrays is a subset of CEL's,
// so a compact JSON encoding is always valid CEL source for these shapes.
func celLiteral(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", errs.Wrap(errs.FlowInvalid, err, "cannot render assertion value as a literal")
	}
	return string(b), nil
}

// celStringLiteral renders s as a quoted, escaped CEL string literal.
func celStringLiteral(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
