package ui

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Handler serves the built SPA embedded under dist/ (see embed.go). Mount
// it directly on the router at both "/ui" and "/ui/*" (e.g. chi's
// r.Get("/ui", h.ServeHTTP) and r.Get("/ui/*", h.ServeHTTP)) rather than
// through http.StripPrefix: Handler already strips the "/ui" prefix
// itself, and needs the real request path to tell "/ui" (redirect) apart
// from "/ui/" (serve index.html).
func Handler() http.Handler {
	return HandlerFS(distDirFS)
}

// HandlerFS is Handler with the embedded dist/ filesystem replaced by
// fsys, so tests can exercise the SPA-serving logic (history fallback,
// cache headers, the bare-/ui redirect) without depending on
// internal/ui/dist holding a real build. fsys's root must hold
// index.html directly, the same shape fs.Sub(distFS, "dist") gives
// Handler -- not a "dist/" or "ui/" prefixed layout.
func HandlerFS(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ui" {
			http.Redirect(w, r, "/ui/", http.StatusFound)
			return
		}

		rel := relPath(r.URL.Path)
		if !isRegularFile(fsys, rel) {
			// A missing build asset is a 404, never the app shell. Vite
			// content-hashes every filename under assets/, so a request
			// for one that isn't here means the tab was loaded from an
			// older build than the binary now serving it (an upgrade
			// replaced the daemon under an open tab). Falling back to
			// index.html there answers a dynamic import for a .js chunk
			// with text/html, which the browser reports as a MIME type
			// error -- unreadable next to the honest 404 that tells the
			// client its build is gone.
			if strings.HasPrefix(rel, assetsPrefix) {
				http.NotFound(w, r)
				return
			}
			// SPA history fallback: any /ui/* path that isn't a real
			// file (a client-side route like /ui/runs/run_123) gets the
			// app shell, which then renders that route itself.
			rel = "index.html"
		}

		setCacheHeaders(w, rel)

		// Clone rather than mutate r.URL in place, so middleware that
		// logs the original request path after ServeHTTP returns (as
		// internal/server's loggingMiddleware does) still sees it.
		inner := r.Clone(r.Context())
		u := *r.URL
		if rel == "index.html" {
			// http.FileServer's serveFile has a special case: a request
			// path ending in "/index.html" gets redirected to the same
			// path with that suffix dropped (avoiding two URLs for one
			// piece of content). That's exactly wrong here -- it would
			// send every SPA-fallback request back to "/ui/" instead of
			// serving the app shell at the path the client asked for --
			// so ask for the directory root instead, which
			// http.FileServer serves index.html for anyway, minus the
			// redirect.
			u.Path = "/"
		} else {
			u.Path = "/" + rel
		}
		inner.URL = &u

		fileServer.ServeHTTP(w, inner)
	})
}

// relPath turns a request path under /ui into a path relative to fsys's
// root ("" only for "/ui/" itself, which becomes "index.html"),
// path.Clean-ing away any ".." segments so a request can't escape fsys
// regardless of what the underlying fs.FS would itself allow.
// assetsPrefix is where Vite writes every content-hashed build artifact.
// Paths under it are both cached forever (setCacheHeaders) and excluded
// from the SPA history fallback (HandlerFS): a hashed name either exists
// in this build or does not exist at all.
const assetsPrefix = "assets/"

func relPath(reqPath string) string {
	rel := strings.TrimPrefix(reqPath, "/ui")
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	if rel == "" || rel == "." {
		return "index.html"
	}
	return rel
}

func isRegularFile(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && info.Mode().IsRegular()
}

// setCacheHeaders implements the caching split PLAN §34c calls for:
// index.html can change without its URL changing (a new build still
// serves it at exactly "/ui/"), so it must always be revalidated; Vite's
// build hashes every filename under assets/*, so those can be cached
// forever.
func setCacheHeaders(w http.ResponseWriter, rel string) {
	switch {
	case rel == "index.html":
		w.Header().Set("Cache-Control", "no-cache")
	case strings.HasPrefix(rel, assetsPrefix):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
}
