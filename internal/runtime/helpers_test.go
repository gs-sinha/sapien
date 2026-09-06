package runtime

import (
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

type stringerID struct{ v string }

func (s stringerID) String() string { return s.v }

func TestStringifyScalarVariants(t *testing.T) {
	assert.Equal(t, "5", stringifyScalar(int32(5)))
	assert.Equal(t, "7", stringifyScalar(uint(7)))
	assert.Equal(t, "9", stringifyScalar(uint64(9)))
	assert.Equal(t, "1.5", stringifyScalar(float32(1.5)))
	assert.Equal(t, "custom-id", stringifyScalar(stringerID{"custom-id"}))
}

func TestJoinEscapedPaths(t *testing.T) {
	assert.Equal(t, "/riders/1", joinEscapedPaths("", "riders/1"))
	assert.Equal(t, "/riders/1", joinEscapedPaths("", "/riders/1"))
	assert.Equal(t, "/api", joinEscapedPaths("/api", "/"))
	assert.Equal(t, "/api", joinEscapedPaths("/api", ""))
	assert.Equal(t, "/api/riders", joinEscapedPaths("/api/", "/riders"))
}

func TestHostOfInvalidURL(t *testing.T) {
	assert.Equal(t, "://bad", hostOf("://bad"))
}

func TestRequestRecordEncodeError(t *testing.T) {
	r := NewRedactor(nil, nil)
	rec := r.RequestRecord(Request{Method: "POST", URL: "https://x", Body: func() {}})
	assert.Nil(t, rec.Body)
	assert.Empty(t, rec.BodyRaw)
}

func TestIsConnRefusedOpError(t *testing.T) {
	// A dial to a closed local listener produces a *net.OpError wrapping
	// ECONNREFUSED, exercising the errors.As unwrap path directly.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	addr := ln.Addr().String()
	assert.NoError(t, ln.Close())

	_, dialErr := net.Dial("tcp", addr)
	assert.Error(t, dialErr)
	assert.True(t, isConnRefused(dialErr))
	assert.True(t, isConnRefused(fmt.Errorf("wrapped: %w", dialErr)), "should detect through error wrapping")
}
