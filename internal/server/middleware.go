package server

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/errs"
)

// isLocalHostname reports whether h (already stripped of any port and
// brackets) is a loopback hostname.
func isLocalHostname(h string) bool {
	switch h {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// hostnameOf extracts the hostname from a Host header value, which may or
// may not include a port (and, for IPv6, brackets).
func hostnameOf(hostHeader string) string {
	h, _, err := net.SplitHostPort(hostHeader)
	if err != nil {
		h = hostHeader
	}
	return strings.Trim(h, "[]")
}

// isAllowedOrigin reports whether origin is a localhost origin or the Tauri
// desktop shell's origin.
func isAllowedOrigin(origin string) bool {
	if origin == "tauri://localhost" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return isLocalHostname(u.Hostname())
}

// hostOriginMiddleware enforces the DNS-rebinding guard (PLAN §28): the Host
// header must name a loopback address (any port), and, when present, Origin
// must be a localhost origin or tauri://localhost. It runs for every route,
// including /v1/health.
func (s *Server) hostOriginMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalHostname(hostnameOf(r.Host)) {
			writeErrorStatus(w, http.StatusForbidden, errs.New(errs.PermissionDenied, "host %q is not a local address", r.Host))
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !isAllowedOrigin(origin) {
			writeErrorStatus(w, http.StatusForbidden, errs.New(errs.PermissionDenied, "origin %q is not allowed", origin))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authMiddleware requires a bearer token matching the server's token,
// read from "Authorization: Bearer <token>" if present, else from the
// sapien_session cookie GET /ui/session sets for the browser UI (PLAN
// §34c, handlers_ui.go). It wraps every route except /v1/health.
//
// A cookie-authenticated request that isn't a GET/HEAD additionally
// requires an Origin header naming an allowed origin. hostOriginMiddleware
// already rejects a *disallowed* Origin whenever one is present, for
// every request; this adds "and it must be present at all" for exactly
// this case, as defence in depth against CSRF: a bearer header can only
// be attached by code that already reads the token -- the CLI, an MCP
// host, a test -- so it needs no such defense, but a cookie rides along
// automatically with any request a browser makes to this origin,
// including one instigated by another site's page, and Origin is the one
// thing on such a cross-site request that page cannot forge.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, viaCookie, ok := s.bearerToken(r)
		if !ok {
			writeErrorStatus(w, http.StatusUnauthorized, errs.New(errs.PermissionDenied, "missing bearer token"))
			return
		}
		if subtle.ConstantTimeCompare([]byte(presented), []byte(s.token)) != 1 {
			writeErrorStatus(w, http.StatusUnauthorized, errs.New(errs.PermissionDenied, "invalid bearer token"))
			return
		}
		if viaCookie && r.Method != http.MethodGet && r.Method != http.MethodHead {
			origin := r.Header.Get("Origin")
			if origin == "" || !isAllowedOrigin(origin) {
				writeErrorStatus(w, http.StatusForbidden, errs.New(errs.PermissionDenied, "origin required for cookie-authenticated %s", r.Method))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// bearerToken extracts the token to check for r: the Authorization header
// if present, else the sapien_session cookie. viaCookie tells
// authMiddleware's CSRF check above which source was used; ok is false
// when neither was present at all (an invalid token, as opposed to a
// missing one, is reported by the caller instead, so both sources fail
// with the same "invalid bearer token" message).
func (s *Server) bearerToken(r *http.Request) (token string, viaCookie bool, ok bool) {
	const prefix = "Bearer "
	if authz := r.Header.Get("Authorization"); strings.HasPrefix(authz, prefix) {
		return strings.TrimPrefix(authz, prefix), false, true
	}
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		return c.Value, true, true
	}
	return "", false, false
}

// statusRecorder captures the status code written by a handler so it can be
// logged; http handlers that never call WriteHeader implicitly answer 200.
//
// Unwrap exposes the underlying ResponseWriter (the http.ResponseController
// convention from Go 1.20+): coder/websocket's Accept needs the
// connection's real http.Hijacker to upgrade GET /v1/events, and an
// embedding wrapper like this one that only overrides WriteHeader does not
// itself implement http.Hijacker. Without Unwrap, every WebSocket upgrade
// downstream of loggingMiddleware would fail with 501.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// loggingMiddleware logs method, path, status, and duration at debug level.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logger.Debug("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start),
		)
	})
}

// idleMiddleware resets the idle countdown after every request completes.
func (s *Server) idleMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		s.idle.touch()
	})
}
