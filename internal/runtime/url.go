package runtime

import (
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/growsimplee/sapien/internal/errs"
)

var pathParamRe = regexp.MustCompile(`\{([^{}]+)\}`)

// BuildURL substitutes {param} placeholders in pathTemplate from pathParams,
// joins the result onto baseURL (which may already carry a path prefix and/or
// a query string), and appends query from the given map. Path param values
// are percent-escaped as a single path segment (so a "/" inside a value
// cannot introduce an extra path segment). Query values may be scalars or
// slices (repeated as ?k=v1&k=v2); nil values are skipped.
func BuildURL(baseURL, pathTemplate string, pathParams map[string]any, query map[string]any) (string, error) {
	var missing string
	escapedPath := pathParamRe.ReplaceAllStringFunc(pathTemplate, func(m string) string {
		name := m[1 : len(m)-1]
		v, ok := pathParams[name]
		if !ok || v == nil {
			missing = name
			return m
		}
		return url.PathEscape(stringifyScalar(v))
	})
	if missing != "" {
		return "", errs.New(errs.InputMissing, "missing path parameter %q", missing).WithDetail("param", missing)
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return "", errs.Wrap(errs.Invalid, err, "invalid base URL %q", baseURL)
	}

	joined := joinEscapedPaths(base.EscapedPath(), escapedPath)
	if err := setEscapedPath(base, joined); err != nil {
		return "", errs.Wrap(errs.Invalid, err, "invalid path %q", joined)
	}

	q := base.Query()
	for k, v := range query {
		addQueryValue(q, k, v)
	}
	base.RawQuery = q.Encode()

	return base.String(), nil
}

// setEscapedPath sets u.Path/u.RawPath from an already-escaped path string,
// so u.String() reproduces escapedPath verbatim (including any %2F we
// inserted for a path-param value containing a literal "/").
func setEscapedPath(u *url.URL, escapedPath string) error {
	decoded, err := url.PathUnescape(escapedPath)
	if err != nil {
		return err
	}
	u.Path = decoded
	u.RawPath = escapedPath
	return nil
}

func joinEscapedPaths(base, add string) string {
	if base == "" {
		if !strings.HasPrefix(add, "/") {
			return "/" + add
		}
		return add
	}
	baseTrim := strings.TrimSuffix(base, "/")
	addTrim := strings.TrimPrefix(add, "/")
	if addTrim == "" {
		return baseTrim
	}
	return baseTrim + "/" + addTrim
}

func addQueryValue(q url.Values, k string, v any) {
	if v == nil {
		return
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		for i := 0; i < rv.Len(); i++ {
			item := rv.Index(i).Interface()
			if item == nil {
				continue
			}
			q.Add(k, stringifyScalar(item))
		}
		return
	}
	q.Add(k, stringifyScalar(v))
}

// stringifyScalar renders a scalar value the way it should appear in a URL:
// numbers without exponent notation, bools as true/false, strings as-is.
func stringifyScalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int32:
		return strconv.FormatInt(int64(t), 10)
	case int64:
		return strconv.FormatInt(t, 10)
	case uint:
		return strconv.FormatUint(uint64(t), 10)
	case uint64:
		return strconv.FormatUint(t, 10)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprintf("%v", t)
	}
}
