package server

import (
	"context"
	"net/http"

	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// WorkspaceHeader names the workspace a request targets, by absolute
// directory. It is optional: a request that omits it gets the daemon's
// primary workspace, which is what every pre-multi-workspace client sends
// and what the one-shot CLI still relies on. `?workspace=` does the same
// thing for the two WebSocket routes, where a browser cannot set headers.
const WorkspaceHeader = "X-Sapien-Workspace"

// workspaceQueryParam is the query-string equivalent of WorkspaceHeader.
const workspaceQueryParam = "workspace"

type engineCtxKey struct{}

// withEngine returns ctx carrying eng as the engine for this request.
func withEngine(ctx context.Context, eng engine.Engine) context.Context {
	return context.WithValue(ctx, engineCtxKey{}, eng)
}

// engineFrom returns the engine resolved for this request by
// workspaceMiddleware. It never returns nil: the middleware rejects a
// request it cannot resolve before any handler runs, so handlers do not
// carry an error path for "which workspace?".
func engineFrom(ctx context.Context) engine.Engine {
	if eng, ok := ctx.Value(engineCtxKey{}).(engine.Engine); ok && eng != nil {
		return eng
	}
	// Unreachable through the router. A handler called directly in a test
	// without the middleware would panic here rather than silently act on
	// the wrong workspace, which is the safer failure.
	panic("server: no engine in request context (workspaceMiddleware not applied)")
}

// targetWorkspace returns the workspace directory a request names, or "" for
// the primary one.
func targetWorkspace(r *http.Request) string {
	if dir := r.Header.Get(WorkspaceHeader); dir != "" {
		return dir
	}
	return r.URL.Query().Get(workspaceQueryParam)
}

// workspaceMiddleware resolves each request's target workspace to an engine
// and puts it in the request context. With no Manager configured (a test
// server, or any single-engine embedding) every request resolves to the one
// engine, and naming a different workspace is refused rather than silently
// served from the wrong one.
func (s *Server) workspaceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dir := targetWorkspace(r)

		if s.workspaces == nil {
			if dir != "" && !s.isPrimaryDir(dir) {
				writeErrorStatus(w, http.StatusNotFound,
					errs.New(errs.WorkspaceNotFound, "this daemon serves a single workspace and cannot switch to %q", dir).
						WithHint("restart the daemon in that workspace, or upgrade it"))
				return
			}
			next.ServeHTTP(w, r.WithContext(withEngine(r.Context(), s.engine)))
			return
		}

		eng, err := s.workspaces.Engine(dir)
		if err != nil {
			writeError(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(withEngine(r.Context(), eng)))
	})
}

// isPrimaryDir reports whether dir names the single engine's own workspace,
// so a client that always sends the header still works against a
// single-workspace server.
func (s *Server) isPrimaryDir(dir string) bool {
	ws := s.engine.Workspace()
	return ws != nil && ws.Dir == dir
}
