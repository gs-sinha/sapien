package server

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemStats_RequiresToken(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/debug/memstats", reqOpts{})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp = doReq(t, ts, http.MethodGet, "/debug/memstats", reqOpts{token: "wrong"})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// The loopback guard covers /debug exactly as it covers /v1: a request
// arriving with a non-local Host is refused before auth is even considered.
func TestMemStats_RejectsNonLocalHost(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/debug/memstats", reqOpts{token: "test-token", host: "evil.example.com"})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestMemStats_ReportsRuntimeNumbers(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/debug/memstats", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var m memStatsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&m))

	assert.Positive(t, m.HeapAlloc)
	assert.Positive(t, m.TotalAlloc)
	assert.Positive(t, m.HeapSys)
	assert.Positive(t, m.NumGoroutine)
	assert.Positive(t, m.GOMAXPROCS)
	assert.GreaterOrEqual(t, m.TotalAlloc, m.HeapAlloc, "total_alloc counts every byte ever allocated")
}

func TestPprof_RequiresTokenAndServesProfiles(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/debug/pprof/heap", reqOpts{})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp = doReq(t, ts, http.MethodGet, "/debug/pprof/heap?debug=1", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body := readBody(t, resp)
	assert.Contains(t, body, "heap profile", "the pprof heap handler did not answer")
}

// The debug endpoints are an operator's tool, not part of the API contract
// clients are told about, so they must stay out of the generated OpenAPI
// document -- publishing Go's pprof wire format as a Sapien API would
// promise a stability that is not ours to give.
func TestOpenAPI_DoesNotAdvertiseDebugRoutes(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/openapi.json", reqOpts{token: "test-token"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, readBody(t, resp), "/debug/")
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(data)
}
