package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/store"
)

func TestMarshalUnmarshalJSON_RoundTrip(t *testing.T) {
	type payload struct {
		Tags []string `json:"tags"`
		N    int      `json:"n"`
	}

	in := payload{Tags: []string{"a", "b"}, N: 3}
	s, err := store.MarshalJSON(in)
	require.NoError(t, err)
	assert.JSONEq(t, `{"tags":["a","b"],"n":3}`, s)

	var out payload
	require.NoError(t, store.UnmarshalJSON(s, &out))
	assert.Equal(t, in, out)
}

func TestUnmarshalJSON_EmptyStringIsNoOp(t *testing.T) {
	out := map[string]any{"seed": true}
	require.NoError(t, store.UnmarshalJSON("", &out))
	assert.Equal(t, map[string]any{"seed": true}, out)
}

func TestMarshalJSON_UnsupportedValueErrors(t *testing.T) {
	_, err := store.MarshalJSON(make(chan int))
	assert.Error(t, err)
}

func TestUnmarshalJSON_InvalidJSONErrors(t *testing.T) {
	var out map[string]any
	err := store.UnmarshalJSON("{not json", &out)
	assert.Error(t, err)
}

func TestNullString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"empty", "", false},
		{"non-empty", "x", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns := store.NullString(tt.input)
			assert.Equal(t, tt.valid, ns.Valid)
			assert.Equal(t, tt.input, store.StringOrEmpty(ns))
		})
	}
}
