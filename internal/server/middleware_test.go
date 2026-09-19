package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsLocalHostname_Table covers isLocalHostname directly (PLAN §34f item
// 3): the classic loopback names, any "*.localhost" name (what `sapien ui`
// opens by default -- see ui.go), and the near-miss names an attacker
// controls that must still be rejected.
func TestIsLocalHostname_Table(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"sapien.localhost", true},
		{"SAPIEN.LOCALHOST", true}, // case-insensitive
		{"a.b.localhost", true},    // any depth under .localhost
		{"localhost.", true},       // one trailing dot tolerated
		{"sapien.localhost.", true},
		{"localhost.evil.com", false}, // "localhost" is a label, not the TLD
		{"evil-localhost", false},     // not a ".localhost" suffix at all
		{"notlocalhost", false},
		{"evil.com", false},
		{"", false},
		{"127.0.0.2", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, isLocalHostname(tc.host), "host %q", tc.host)
	}
}

// TestIsAllowedOrigin_Table covers isAllowedOrigin, which layers URL
// parsing and the Tauri desktop shell's fixed origin on top of
// isLocalHostname.
func TestIsAllowedOrigin_Table(t *testing.T) {
	cases := []struct {
		origin string
		want   bool
	}{
		{"tauri://localhost", true},
		{"http://localhost:7717", true},
		{"http://127.0.0.1:7717", true},
		{"http://sapien.localhost:7717", true},
		{"http://SAPIEN.LOCALHOST:7717", true},
		{"https://sapien.localhost", true},
		{"http://localhost.evil.com", false},
		{"http://evil-localhost", false},
		{"http://evil.com", false},
		{"not a url", false},
		{"", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, isAllowedOrigin(tc.origin), "origin %q", tc.origin)
	}
}

// TestHostOriginMiddleware_AcceptsWildcardLocalhost is the end-to-end
// counterpart of the table tests above, through the real middleware stack
// (see also TestLoopbackHostsAccepted/TestAllowedOriginsAccepted in
// server_test.go, which this deliberately does not duplicate beyond the
// new *.localhost case).
func TestHostOriginMiddleware_AcceptsWildcardLocalhost(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/health", reqOpts{host: "sapien.localhost:9999", origin: "http://sapien.localhost:9999"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHostOriginMiddleware_RejectsLocalhostLookalike(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp := doReq(t, ts, http.MethodGet, "/v1/health", reqOpts{host: "localhost.evil.com"})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}
