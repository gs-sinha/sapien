package search

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestURLMatchScore(t *testing.T) {
	cases := []struct {
		template, path string
		want           float64
	}{
		{"/v1/orders", "/v1/orders", urlScoreExact},
		{"/v1/orders/", "/v1/orders", urlScoreExact},
		{"/v1/orders/{orderId}", "/v1/orders/O1", urlScoreTemplated - urlParamPenalty},
		{"/v1/orders/{orderId}", "/base/v1/orders/O1", urlScoreSuffix - urlParamPenalty},
		{"/v1/riders/search", "/base/v1/riders/search", urlScoreSuffix},
		{"/v1/riders/{riderId}", "/base/v1/riders/search", urlScoreSuffix - urlParamPenalty},
		{"/v1/orders/{orderId}", "/v1/orders", urlScoreParent - urlParentStep},
		{"/v1/orders", "/v1/orders/O1/extra", urlScorePrefix},
		{"/v1/orders/{orderId}", "/v1/riders/R1", 0},
		{"/v1/orders/{orderId}", "/v1/orders/O1/items", urlScorePrefix - urlParamPenalty},
	}
	for _, tc := range cases {
		t.Run(tc.template+" vs "+tc.path, func(t *testing.T) {
			assert.InDelta(t, tc.want, urlMatchScore(tc.template, tc.path), 0.0001)
		})
	}
}
