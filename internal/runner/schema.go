package runner

import (
	"fmt"
	"math"
	"reflect"
	"sort"

	"github.com/gs-sinha/sapien/internal/domain"
)

// ValidateSchema validates value against the normalized domain.Schema s, for
// the `schema: contract` assertion. It returns a problem for every violation
// found, each naming the offending path (e.g.
// "body.rider.online: expected boolean, got string"); nil means value is
// valid. The top-level path is always "body", since this validator's only
// caller checks a response body against its declared schema.
func ValidateSchema(s *domain.Schema, value any) []string {
	if s == nil {
		return nil
	}
	return validateAt(s, value, "body")
}

func validateAt(s *domain.Schema, value any, path string) []string {
	if s == nil {
		return nil
	}

	switch s.Kind {
	case domain.KindAny, domain.KindRef:
		return nil
	}

	if value == nil {
		if s.Nullable || s.Kind == domain.KindNull {
			return nil
		}
		return []string{fmt.Sprintf("%s: expected %s, got null", path, s.Kind)}
	}

	switch s.Kind {
	case domain.KindNull:
		return []string{fmt.Sprintf("%s: expected null, got %s", path, typeName(value))}

	case domain.KindObject:
		return validateObject(s, value, path)

	case domain.KindArray:
		return validateArray(s, value, path)

	case domain.KindString:
		if _, ok := value.(string); !ok {
			return []string{fmt.Sprintf("%s: expected string, got %s", path, typeName(value))}
		}
		return enumProblems(s, value, path)

	case domain.KindBoolean:
		if _, ok := value.(bool); !ok {
			return []string{fmt.Sprintf("%s: expected boolean, got %s", path, typeName(value))}
		}
		return enumProblems(s, value, path)

	case domain.KindInteger:
		if !isInteger(value) {
			if isNumber(value) {
				return []string{fmt.Sprintf("%s: expected integer, got non-integral number", path)}
			}
			return []string{fmt.Sprintf("%s: expected integer, got %s", path, typeName(value))}
		}
		return enumProblems(s, value, path)

	case domain.KindNumber:
		if !isNumber(value) {
			return []string{fmt.Sprintf("%s: expected number, got %s", path, typeName(value))}
		}
		return enumProblems(s, value, path)

	case domain.KindOneOf, domain.KindAnyOf:
		return validateAnyVariant(s, value, path)

	case domain.KindAllOf:
		return validateAllVariants(s, value, path)

	default:
		return nil
	}
}

func validateObject(s *domain.Schema, value any, path string) []string {
	m, ok := value.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%s: expected object, got %s", path, typeName(value))}
	}

	var problems []string
	for _, name := range s.Required {
		if _, present := m[name]; !present {
			problems = append(problems, fmt.Sprintf("%s: missing required property %q", path, name))
		}
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := m[k]
		childPath := path + "." + k
		if child, ok := s.Properties[k]; ok {
			problems = append(problems, validateAt(child, v, childPath)...)
			continue
		}
		if s.AdditionalProperties != nil {
			problems = append(problems, validateAt(s.AdditionalProperties, v, childPath)...)
		}
	}
	return problems
}

func validateArray(s *domain.Schema, value any, path string) []string {
	arr, ok := value.([]any)
	if !ok {
		return []string{fmt.Sprintf("%s: expected array, got %s", path, typeName(value))}
	}
	if s.Items == nil {
		return nil
	}
	var problems []string
	for i, item := range arr {
		problems = append(problems, validateAt(s.Items, item, fmt.Sprintf("%s[%d]", path, i))...)
	}
	return problems
}

func validateAnyVariant(s *domain.Schema, value any, path string) []string {
	if len(s.Variants) == 0 {
		return nil
	}
	for _, v := range s.Variants {
		if len(validateAt(v, value, path)) == 0 {
			return nil
		}
	}
	return []string{fmt.Sprintf("%s: value does not match any of %d variants", path, len(s.Variants))}
}

func validateAllVariants(s *domain.Schema, value any, path string) []string {
	var problems []string
	for _, v := range s.Variants {
		problems = append(problems, validateAt(v, value, path)...)
	}
	return problems
}

func enumProblems(s *domain.Schema, value any, path string) []string {
	if len(s.Enum) == 0 {
		return nil
	}
	for _, e := range s.Enum {
		if enumMatch(value, e) {
			return nil
		}
	}
	return []string{fmt.Sprintf("%s: value %v not in enum %v", path, value, s.Enum)}
}

func enumMatch(v, want any) bool {
	if isNumber(v) && isNumber(want) {
		return toFloat(v) == toFloat(want)
	}
	if reflect.DeepEqual(v, want) {
		return true
	}
	return fmt.Sprintf("%v", v) == fmt.Sprintf("%v", want)
}

func isNumber(v any) bool {
	switch v.(type) {
	case int, int32, int64, float32, float64:
		return true
	default:
		return false
	}
}

func isInteger(v any) bool {
	switch t := v.(type) {
	case int, int32, int64:
		return true
	case float64:
		return !math.IsInf(t, 0) && !math.IsNaN(t) && t == math.Trunc(t)
	case float32:
		f := float64(t)
		return !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f)
	default:
		return false
	}
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case int:
		return float64(t)
	case int32:
		return float64(t)
	case int64:
		return float64(t)
	case float32:
		return float64(t)
	case float64:
		return t
	default:
		return 0
	}
}

func typeName(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case int, int32, int64:
		return "integer"
	case float32, float64:
		return "number"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", v)
	}
}
