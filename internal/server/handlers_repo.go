package server

import "net/http"

// handleWorkspaceRepoStatus implements GET /v1/workspace/repo: the
// workspace's own git repository (PLAN §7b), read from refs already on
// disk -- no network.
func (s *Server) handleWorkspaceRepoStatus(w http.ResponseWriter, r *http.Request) {
	out, err := engineFrom(r.Context()).Repo().Status(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleWorkspaceRepoFetch implements POST /v1/workspace/repo/fetch: `git
// fetch`, refreshing Behind/Ahead/FetchedAt; the working tree is
// untouched.
func (s *Server) handleWorkspaceRepoFetch(w http.ResponseWriter, r *http.Request) {
	out, err := engineFrom(r.Context()).Repo().Fetch(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleWorkspaceRepoPull implements POST /v1/workspace/repo/pull:
// fast-forwards onto the upstream. The engine refuses (errs.Conflict) a
// dirty tree, a missing upstream, or a diverged branch; writeError maps
// that to 409, same as every other engine error.
func (s *Server) handleWorkspaceRepoPull(w http.ResponseWriter, r *http.Request) {
	out, err := engineFrom(r.Context()).Repo().Pull(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleWorkspaceRepoSync implements POST /v1/workspace/repo/sync: fetch,
// then pull when the tree is clean and behind; otherwise a status whose
// Skipped says why nothing moved. Only a fetch or git failure is an error
// here -- an unpulled tree is not.
func (s *Server) handleWorkspaceRepoSync(w http.ResponseWriter, r *http.Request) {
	out, err := engineFrom(r.Context()).Repo().Sync(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleWorkspaceRepoPush implements POST /v1/workspace/repo/push: sends
// the branch's unpushed commits to its upstream. The engine refuses
// (errs.Conflict) a branch that is behind, since a pull must come first;
// writeError maps that to 409 like every other engine error. A no-op
// success (Pushed false) when nothing is ahead.
func (s *Server) handleWorkspaceRepoPush(w http.ResponseWriter, r *http.Request) {
	out, err := engineFrom(r.Context()).Repo().Push(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
