package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseJSONBody(t *testing.T) {
	t.Run("ints and floats preserved", func(t *testing.T) {
		v, ok := ParseJSONBody([]byte(`{"count": 2, "price": 9.5}`))
		require.True(t, ok)
		m, ok := v.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, int64(2), m["count"])
		assert.Equal(t, 9.5, m["price"])
	})

	t.Run("nested arrays and objects", func(t *testing.T) {
		v, ok := ParseJSONBody([]byte(`{"items":[{"n":1},{"n":2.5}]}`))
		require.True(t, ok)
		m := v.(map[string]any)
		items := m["items"].([]any)
		require.Len(t, items, 2)
		assert.Equal(t, int64(1), items[0].(map[string]any)["n"])
		assert.Equal(t, 2.5, items[1].(map[string]any)["n"])
	})

	t.Run("empty body", func(t *testing.T) {
		_, ok := ParseJSONBody(nil)
		assert.False(t, ok)
		_, ok = ParseJSONBody([]byte("   "))
		assert.False(t, ok)
	})

	t.Run("non-JSON body", func(t *testing.T) {
		_, ok := ParseJSONBody([]byte("not json at all"))
		assert.False(t, ok)
	})

	t.Run("trailing garbage rejected", func(t *testing.T) {
		_, ok := ParseJSONBody([]byte(`{"a":1} garbage`))
		assert.False(t, ok)
	})

	t.Run("scalar JSON values", func(t *testing.T) {
		v, ok := ParseJSONBody([]byte(`42`))
		require.True(t, ok)
		assert.Equal(t, int64(42), v)

		v, ok = ParseJSONBody([]byte(`"hello"`))
		require.True(t, ok)
		assert.Equal(t, "hello", v)

		v, ok = ParseJSONBody([]byte(`true`))
		require.True(t, ok)
		assert.Equal(t, true, v)
	})
}

func TestEncodeBody(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		data, isJSON, err := encodeBody(nil)
		require.NoError(t, err)
		assert.Nil(t, data)
		assert.False(t, isJSON)
	})

	t.Run("bytes as-is", func(t *testing.T) {
		data, isJSON, err := encodeBody([]byte("raw"))
		require.NoError(t, err)
		assert.Equal(t, "raw", string(data))
		assert.False(t, isJSON)
	})

	t.Run("string as-is", func(t *testing.T) {
		data, isJSON, err := encodeBody(`{"already":"json-looking"}`)
		require.NoError(t, err)
		assert.Equal(t, `{"already":"json-looking"}`, string(data))
		assert.False(t, isJSON)
	})

	t.Run("struct JSON-encoded", func(t *testing.T) {
		data, isJSON, err := encodeBody(map[string]any{"a": 1})
		require.NoError(t, err)
		assert.True(t, isJSON)
		assert.JSONEq(t, `{"a":1}`, string(data))
	})
}
