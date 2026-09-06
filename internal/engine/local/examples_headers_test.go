package local

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gs-sinha/sapien/internal/domain"
)

func TestLiftHeaderParams(t *testing.T) {
	op := &domain.Operation{Params: []domain.Param{
		{Name: "orgId", In: domain.InHeader, Required: true},
		{Name: "riderId", In: domain.InPath, Required: true},
	}}

	t.Run("canonicalised header moves into input under the declared name", func(t *testing.T) {
		input, headers := liftHeaderParams(op, nil, map[string]string{"Orgid": "1", "X-Custom": "keep"})
		assert.Equal(t, map[string]any{"orgId": "1"}, input)
		assert.Equal(t, map[string]string{"X-Custom": "keep"}, headers)
	})

	t.Run("input already has it: header dropped, input untouched", func(t *testing.T) {
		input, headers := liftHeaderParams(op, map[string]any{"OrgId": "7"}, map[string]string{"orgid": "1"})
		assert.Equal(t, map[string]any{"OrgId": "7"}, input)
		assert.Nil(t, headers)
	})

	t.Run("nil op or no headers is a no-op", func(t *testing.T) {
		input, headers := liftHeaderParams(nil, map[string]any{"a": 1}, map[string]string{"Orgid": "1"})
		assert.Equal(t, map[string]any{"a": 1}, input)
		assert.Equal(t, map[string]string{"Orgid": "1"}, headers)
		input, headers = liftHeaderParams(op, nil, nil)
		assert.Nil(t, input)
		assert.Nil(t, headers)
	})
}

func TestFilterExampleHeaders_DropsTransportNoise(t *testing.T) {
	got := filterExampleHeaders(map[string]string{
		"User-Agent":      "sapien/dev",
		"Content-Type":    "application/json",
		"Content-Length":  "42",
		"Accept":          "*/*",
		"X-Request-Id":    "abc",
		"Traceparent":     "00-1-2-01",
		"Authorization":   "Bearer x",
		"X-Custom":        "keep",
		"Idempotency-Key": "k1",
	})
	assert.Equal(t, map[string]string{"X-Custom": "keep", "Idempotency-Key": "k1"}, got)
}
