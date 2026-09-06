package textutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseURLQuery(t *testing.T) {
	cases := []struct {
		in     string
		method string
		path   string
		ok     bool
	}{
		{"POST /v1/orders", "POST", "/v1/orders", true},
		{"/v1/orders?x=1&y=2", "", "/v1/orders", true},
		{"/v1/orders/", "", "/v1/orders", true},
		{"v1/orders/123", "", "/v1/orders/123", true},
		{"https://api.example.com/base/v1/orders#frag", "", "/base/v1/orders", true},
		{"GET https://api.example.com:8443/v1/riders/R1?expand=1", "GET", "/v1/riders/R1", true},
		{"http://host", "", "/", true},
		{"/a//b///c", "", "/a/b/c", true},
		{"order-service.createOrder", "", "", false},
		{"find riders near pickup", "", "", false},
		{"post orders", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			m, p, ok := ParseURLQuery(tc.in)
			assert.Equal(t, tc.ok, ok)
			if ok {
				assert.Equal(t, tc.method, m)
				assert.Equal(t, tc.path, p)
			}
		})
	}
}

func TestPathSegmentsAndParams(t *testing.T) {
	assert.Equal(t, []string{"v1", "riders", "{riderId}"}, PathSegments("/v1/riders/{riderId}/"))
	assert.Nil(t, PathSegments("/"))
	assert.True(t, IsPathParam("{id}"))
	assert.False(t, IsPathParam("id"))
	assert.False(t, IsPathParam("{}"))
}
