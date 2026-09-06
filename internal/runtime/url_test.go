package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/errs"
)

func TestBuildURL(t *testing.T) {
	t.Run("basic substitution", func(t *testing.T) {
		u, err := BuildURL("https://api.example.com", "/v1/riders/{riderId}", map[string]any{"riderId": "r1"}, nil)
		require.NoError(t, err)
		assert.Equal(t, "https://api.example.com/v1/riders/r1", u)
	})

	t.Run("space and slash in path param are escaped", func(t *testing.T) {
		u, err := BuildURL("https://api.example.com", "/v1/riders/{riderId}", map[string]any{"riderId": "a b/c"}, nil)
		require.NoError(t, err)
		assert.Equal(t, "https://api.example.com/v1/riders/a%20b%2Fc", u)
	})

	t.Run("numbers and bools stringified without exponent", func(t *testing.T) {
		u, err := BuildURL("https://api.example.com", "/v1/things/{id}/{active}", map[string]any{
			"id":     1000000.0,
			"active": true,
		}, nil)
		require.NoError(t, err)
		assert.Equal(t, "https://api.example.com/v1/things/1000000/true", u)
	})

	t.Run("missing path param", func(t *testing.T) {
		_, err := BuildURL("https://api.example.com", "/v1/riders/{riderId}", nil, nil)
		require.Error(t, err)
		e := errs.As(err)
		assert.Equal(t, errs.InputMissing, e.Code)
		assert.Equal(t, "riderId", e.Details["param"])
	})

	t.Run("nil path param value counts as missing", func(t *testing.T) {
		_, err := BuildURL("https://api.example.com", "/v1/riders/{riderId}", map[string]any{"riderId": nil}, nil)
		require.Error(t, err)
		assert.True(t, errs.Is(err, errs.InputMissing))
	})

	t.Run("repeated query values", func(t *testing.T) {
		u, err := BuildURL("https://api.example.com", "/v1/search", nil, map[string]any{
			"tag": []any{"a", "b"},
		})
		require.NoError(t, err)
		assert.Equal(t, "https://api.example.com/v1/search?tag=a&tag=b", u)
	})

	t.Run("nil query values skipped", func(t *testing.T) {
		u, err := BuildURL("https://api.example.com", "/v1/search", nil, map[string]any{
			"q":      "hello",
			"filter": nil,
		})
		require.NoError(t, err)
		assert.Equal(t, "https://api.example.com/v1/search?q=hello", u)
	})

	t.Run("base URL with path prefix and existing query preserved", func(t *testing.T) {
		u, err := BuildURL("https://api.example.com/api/v2?tenant=acme", "/riders/{id}", map[string]any{"id": "r9"}, map[string]any{
			"expand": "profile",
		})
		require.NoError(t, err)
		assert.Equal(t, "https://api.example.com/api/v2/riders/r9?expand=profile&tenant=acme", u)
	})

	t.Run("scalar query numeric formatting", func(t *testing.T) {
		u, err := BuildURL("https://api.example.com", "/v1/things", nil, map[string]any{
			"price": 9.50,
			"count": int64(3),
		})
		require.NoError(t, err)
		assert.Equal(t, "https://api.example.com/v1/things?count=3&price=9.5", u)
	})

	t.Run("no placeholders in template", func(t *testing.T) {
		u, err := BuildURL("https://api.example.com", "/v1/health", nil, nil)
		require.NoError(t, err)
		assert.Equal(t, "https://api.example.com/v1/health", u)
	})
}
