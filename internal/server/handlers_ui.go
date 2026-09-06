package server

import (
	"crypto/subtle"
	"net/http"

	"github.com/gs-sinha/sapien/internal/errs"
)

// sessionCookieName is the HttpOnly cookie GET /ui/session sets, and the
// name authMiddleware (middleware.go) falls back to when there is no
// Authorization header (PLAN §34c).
const sessionCookieName = "sapien_session"

// handleUISession implements GET /ui/session?token=<bearer>: it exchanges
// the daemon's bearer token, presented as a query parameter because
// that's the one credential `sapien ui` can hand the browser before any
// of the UI's own JavaScript has run, for an HttpOnly session cookie,
// then redirects to /ui/. Unlike every route in routeTable, this one is
// not wrapped in authMiddleware -- a query parameter cannot carry an
// "Authorization: Bearer" header, and the token comparison below is
// itself the auth check.
func (s *Server) handleUISession(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
		writeErrorStatus(w, http.StatusUnauthorized, errs.New(errs.PermissionDenied, "invalid or missing token"))
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		// No Secure: the daemon only ever listens on 127.0.0.1 in plain
		// HTTP (PLAN §28's loopback-only guard is the actual security
		// boundary here); marking the cookie Secure on a non-TLS origin
		// would make the browser refuse to store it at all.
	})
	http.Redirect(w, r, "/ui/", http.StatusFound)
}
