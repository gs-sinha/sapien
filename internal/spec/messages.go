package spec

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// describeKind renders a jsonschema.ErrorKind as a short, human message
// without the "at /instance/pointer:" prefix the library's own Error()
// produces -- callers attach location separately via Problem.Path/Line.
//
// This is a small hand-rolled formatter (rather than calling
// ek.LocalizedString via golang.org/x/text/message) covering the ErrorKind
// values that can actually reach a leaf of the validation tree for our five
// schemas: container kinds like Group, AllOf/AnyOf/OneOf, and $ref/Reference
// always carry at least one Cause, so collectLeaves recurses through them
// and they never need a message of their own here.
func describeKind(ek jsonschema.ErrorKind) string {
	switch k := ek.(type) {
	case *kind.Type:
		return fmt.Sprintf("must be %s, got %s", strings.Join(k.Want, " or "), k.Got)
	case *kind.Required:
		if len(k.Missing) == 1 {
			return fmt.Sprintf("missing required property %s", quote(k.Missing[0]))
		}
		return fmt.Sprintf("missing required properties %s", quoteJoin(k.Missing))
	case *kind.AdditionalProperties:
		if len(k.Properties) == 1 {
			return fmt.Sprintf("additional property %s not allowed", quote(k.Properties[0]))
		}
		return fmt.Sprintf("additional properties %s not allowed", quoteJoin(k.Properties))
	case *kind.Pattern:
		return fmt.Sprintf("does not match pattern %s", quote(k.Want))
	case *kind.Enum:
		return fmt.Sprintf("must be one of %s", joinAny(k.Want))
	case *kind.Const:
		return fmt.Sprintf("must equal %v", k.Want)
	case *kind.Format:
		return fmt.Sprintf("invalid %s: %v", k.Want, k.Err)
	case *kind.MinLength:
		return fmt.Sprintf("length must be >= %d", k.Want)
	case *kind.MaxLength:
		return fmt.Sprintf("length must be <= %d", k.Want)
	case *kind.MinItems:
		return fmt.Sprintf("must have at least %d item(s)", k.Want)
	case *kind.MaxItems:
		return fmt.Sprintf("must have at most %d item(s)", k.Want)
	case *kind.MinProperties:
		return fmt.Sprintf("must have at least %d %s", k.Want, plural(k.Want, "property", "properties"))
	case *kind.MaxProperties:
		return fmt.Sprintf("must have at most %d %s", k.Want, plural(k.Want, "property", "properties"))
	case *kind.Minimum:
		return fmt.Sprintf("must be >= %s", ratStr(k.Want))
	case *kind.Maximum:
		return fmt.Sprintf("must be <= %s", ratStr(k.Want))
	case *kind.ExclusiveMinimum:
		return fmt.Sprintf("must be > %s", ratStr(k.Want))
	case *kind.ExclusiveMaximum:
		return fmt.Sprintf("must be < %s", ratStr(k.Want))
	case *kind.MultipleOf:
		return fmt.Sprintf("must be a multiple of %s", ratStr(k.Want))
	case *kind.UniqueItems:
		return "items must be unique"
	case *kind.FalseSchema:
		return "not allowed here"
	case *kind.InvalidJsonValue:
		return fmt.Sprintf("unsupported value type %T", k.Value)
	case *kind.RefCycle:
		return "reference cycle in schema"
	default:
		return fmt.Sprintf("failed schema constraint (%T)", ek)
	}
}

func quote(s string) string {
	return strconv.Quote(s)
}

func quoteJoin(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = quote(s)
	}
	return strings.Join(q, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// ratStr renders a *big.Rat (jsonschema/v6 keeps numeric bounds as exact
// rationals) the way an author would write it: as a plain decimal when one
// exists at reasonable precision, falling back to the Rat's own form.
func ratStr(r *big.Rat) string {
	if r == nil {
		return "?"
	}
	if f, exact := r.Float64(); exact {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return r.RatString()
}

func joinAny(vs []any) string {
	ss := make([]string, len(vs))
	for i, v := range vs {
		ss[i] = fmt.Sprintf("%v", v)
	}
	return strings.Join(ss, ", ")
}
