package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/gitsrc"
	"github.com/gs-sinha/sapien/internal/workspace"
	"github.com/gs-sinha/sapien/internal/workspaces"
)

// handleWorkspacesList implements GET /v1/workspaces: every workspace this
// daemon can serve, so a client can offer a switcher without knowing any
// paths in advance. A single-workspace daemon reports just its own, which
// keeps the route meaningful for an older embedding rather than empty.
func (s *Server) handleWorkspacesList(w http.ResponseWriter, r *http.Request) {
	if s.workspaces != nil {
		writeJSON(w, http.StatusOK, s.workspaces.List())
		return
	}

	var out []workspaces.Info
	if ws := s.engine.Workspace(); ws != nil {
		out = append(out, workspaces.Info{
			Dir:      ws.Dir,
			Name:     ws.Name,
			Primary:  true,
			Open:     true,
			Services: len(ws.Services),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleWorkspaceRegister implements POST /v1/workspaces: register a
// workspace directory so it shows up in the list, and open it now so the
// caller learns immediately whether it actually loads. It never creates a
// workspace -- `sapien init` does that.
func (s *Server) handleWorkspaceRegister(w http.ResponseWriter, r *http.Request) {
	var req registerWorkspaceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.Dir == "" {
		writeError(w, errs.New(errs.Invalid, "dir is required"))
		return
	}
	if s.workspaces == nil {
		writeError(w, errs.New(errs.Conflict, "this daemon serves a single workspace").
			WithHint("restart it with a build that supports multiple workspaces"))
		return
	}

	eng, err := s.workspaces.Register(req.Dir)
	if err != nil {
		writeError(w, err)
		return
	}

	var ws *domain.Workspace
	if ws = eng.Workspace(); ws == nil {
		writeError(w, errs.New(errs.Internal, "workspace %q opened without metadata", req.Dir))
		return
	}

	writeJSON(w, http.StatusOK, workspaces.Info{
		Dir:      ws.Dir,
		Name:     ws.Name,
		Primary:  ws.Dir == s.workspaces.Primary(),
		Open:     true,
		Services: len(ws.Services),
	})
}

// handleWorkspaceClose implements DELETE /v1/workspaces?dir=: close that
// workspace's engine on this daemon and release its lock. It does not
// unregister it (`sapien workspace forget` does that, and calls this), so
// a later request naming a still-registered workspace reopens it.
func (s *Server) handleWorkspaceClose(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		writeError(w, errs.New(errs.Invalid, "dir is required"))
		return
	}
	if s.workspaces == nil {
		writeError(w, errs.New(errs.Conflict, "this daemon serves a single workspace").
			WithHint("stop the daemon instead"))
		return
	}
	if err := s.workspaces.CloseOne(dir); err != nil {
		writeError(w, err)
		return
	}
	writeNoContent(w)
}

// handleWorkspaceCreate implements POST /v1/workspaces/create: initialize a
// brand-new workspace directory (sapien.workspace.yaml and the standard
// layout), optionally `git init` it, then register and open it. Unlike
// POST /v1/workspaces, which registers a directory that already is a
// workspace, this flow creates the folder, so an existing non-empty
// directory is refused rather than adopted.
func (s *Server) handleWorkspaceCreate(w http.ResponseWriter, r *http.Request) {
	var req createWorkspaceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.Dir == "" {
		writeError(w, errs.New(errs.Invalid, "dir is required"))
		return
	}
	if s.workspaces == nil {
		writeError(w, errs.New(errs.Conflict, "this daemon serves a single workspace").
			WithHint("restart it with a build that supports multiple workspaces"))
		return
	}

	dir, err := resolveUserPath(req.Dir)
	if err != nil {
		writeError(w, err)
		return
	}

	created := false
	info, statErr := os.Stat(dir)
	switch {
	case statErr == nil:
		if !info.IsDir() {
			writeError(w, errs.New(errs.Invalid, "%s is not a directory", dir).WithDetail("dir", dir))
			return
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			writeError(w, errs.Wrap(errs.Internal, readErr, "reading %s", dir))
			return
		}
		// A non-empty directory is adoptable only when it is already a
		// workspace: that is a retry after Init succeeded and a later step
		// (git init, Register) failed, and refusing it would leave the
		// half-finished result unrecoverable.
		if len(entries) > 0 && !hasWorkspaceFile(dir) {
			writeError(w, errs.New(errs.Conflict, "directory %s already exists and is not empty", dir).
				WithDetail("dir", dir).
				WithHint("choose a new directory, or register the existing workspace instead"))
			return
		}
	case os.IsNotExist(statErr):
		created = true
	default:
		writeError(w, errs.Wrap(errs.Internal, statErr, "checking %s", dir))
		return
	}

	if !hasWorkspaceFile(dir) {
		if _, err := workspace.Init(dir, req.Name); err != nil {
			removeIfCreated(dir, created)
			writeError(w, err)
			return
		}
	}
	if req.GitInit {
		if err := s.git.InitRepo(r.Context(), dir); err != nil {
			removeIfCreated(dir, created)
			writeError(w, err)
			return
		}
	}

	s.registerAndRespond(w, dir, created)
}

// handleWorkspaceClone implements POST /v1/workspaces/clone: clone a
// repository into a new directory, initialize a workspace in it when the
// checkout does not already carry one, then register and open it.
func (s *Server) handleWorkspaceClone(w http.ResponseWriter, r *http.Request) {
	var req cloneWorkspaceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.URL == "" {
		writeError(w, errs.New(errs.Invalid, "url is required"))
		return
	}
	if req.Dir == "" {
		writeError(w, errs.New(errs.Invalid, "dir is required"))
		return
	}
	if s.workspaces == nil {
		writeError(w, errs.New(errs.Conflict, "this daemon serves a single workspace").
			WithHint("restart it with a build that supports multiple workspaces"))
		return
	}
	if !gitsrc.IsGitURL(req.URL) {
		writeError(w, errs.New(errs.Invalid, "not a git url: %s", req.URL).
			WithDetail("url", req.URL).
			WithHint("git sources must be an SSH, HTTPS, git://, or file:// URL"))
		return
	}

	dir, err := resolveUserPath(req.Dir)
	if err != nil {
		writeError(w, err)
		return
	}

	// A directory that did not exist (or was empty) is filled by the clone,
	// so it is ours to remove if a later step fails; a completed clone is
	// adopted instead so a retry does not hit CloneInto's non-empty refusal.
	created := false
	if info, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		created = true
	} else if statErr == nil && info.IsDir() {
		if entries, readErr := os.ReadDir(dir); readErr == nil && len(entries) == 0 {
			created = true
		}
	}

	if looksClonedWorkspace(dir) {
		// Adopt only a clone of THIS repository. A destination that already
		// holds some other workspace's checkout is a name collision, not a
		// retry: opening it would report success for a URL that was never
		// cloned.
		existing, derr := s.git.Describe(r.Context(), dir)
		if derr != nil || existing == nil || gitsrc.NormalizeRemote(existing.Remote) != gitsrc.NormalizeRemote(req.URL) {
			remote := ""
			if existing != nil {
				remote = existing.Remote
			}
			writeError(w, errs.New(errs.Conflict, "%s already holds a checkout of another repository", dir).
				WithDetail("dir", dir).WithDetail("origin", remote).
				WithHint("choose another folder for this clone, or open that one with Existing"))
			return
		}
	} else {
		if err := s.git.CloneInto(r.Context(), req.URL, dir); err != nil {
			removeIfCreated(dir, created)
			writeError(w, err)
			return
		}
	}
	if !hasWorkspaceFile(dir) {
		if _, err := workspace.Init(dir, req.Name); err != nil {
			removeIfCreated(dir, created)
			writeError(w, err)
			return
		}
	}

	s.registerAndRespond(w, dir, created)
}

// registerAndRespond registers dir with the workspace manager and writes its
// workspaces.Info, the shared tail of create and clone (and the same shape
// handleWorkspaceRegister writes). created reports that the directory did not
// exist (or was empty) before this request, so a registration failure removes
// it rather than leaving a half-finished workspace that blocks a retry.
func (s *Server) registerAndRespond(w http.ResponseWriter, dir string, created bool) {
	eng, err := s.workspaces.Register(dir)
	if err != nil {
		removeIfCreated(dir, created)
		writeError(w, err)
		return
	}
	ws := eng.Workspace()
	if ws == nil {
		writeError(w, errs.New(errs.Internal, "workspace %q opened without metadata", dir))
		return
	}
	writeJSON(w, http.StatusOK, workspaces.Info{
		Dir:      ws.Dir,
		Name:     ws.Name,
		Primary:  ws.Dir == s.workspaces.Primary(),
		Open:     true,
		Services: len(ws.Services),
	})
}

// removeIfCreated removes dir when this request created it, best effort: a
// failure here only leaves the half-finished directory the caller would have
// had anyway, and must not mask the original error.
func removeIfCreated(dir string, created bool) {
	if created {
		_ = os.RemoveAll(dir)
	}
}

// hasWorkspaceFile reports whether dir already carries a committed workspace
// file, i.e. workspace.Init has run there.
func hasWorkspaceFile(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, domain.WorkspaceFileName))
	return err == nil && !info.IsDir()
}

// looksClonedWorkspace reports whether dir is a completed workspace clone: a
// git checkout that also carries a workspace file. Used to adopt the result
// of a clone whose later step failed, rather than re-cloning into it.
func looksClonedWorkspace(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false
	}
	return hasWorkspaceFile(dir)
}

// fsDirListing is GET /v1/fs/dirs' response: one directory's immediate
// subdirectories, for the UI's folder picker.
type fsDirListing struct {
	Path    string       `json:"path"`
	Parent  string       `json:"parent,omitempty"`
	Entries []fsDirEntry `json:"entries"`
}

type fsDirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// handleFSDirs implements GET /v1/fs/dirs?path=: the subdirectories of one
// directory (directories only, names starting with "." and "node_modules"
// skipped, sorted case-insensitively), so the UI can offer a folder picker.
// An empty path means the home directory.
func (s *Server) handleFSDirs(w http.ResponseWriter, r *http.Request) {
	root, err := resolveUserPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, err)
		return
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		writeError(w, errs.New(errs.Invalid, "not a directory: %s", root).WithDetail("path", root))
		return
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		writeError(w, errs.Wrap(errs.Internal, err, "reading %s", root))
		return
	}

	out := make([]fsDirEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		out = append(out, fsDirEntry{Name: name, Path: filepath.Join(root, name)})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	listing := fsDirListing{Path: root, Entries: out}
	if parent := filepath.Dir(root); parent != root {
		listing.Parent = parent
	}
	writeJSON(w, http.StatusOK, listing)
}

// resolveUserPath resolves a user-supplied directory: "" means the home
// directory, a leading "~" is expanded, and the result is made absolute. A
// relative path is refused: filepath.Abs would resolve it against the
// daemon's own working directory, which is not what any caller means.
func resolveUserPath(p string) (string, error) {
	switch {
	case p == "":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errs.Wrap(errs.Internal, err, "resolving home directory")
		}
		p = home
	case p == "~" || strings.HasPrefix(p, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errs.Wrap(errs.Internal, err, "resolving home directory")
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	case !filepath.IsAbs(p):
		return "", errs.New(errs.Invalid, "path must be absolute or start with ~: %s", p).
			WithDetail("path", p).
			WithHint("the folder picker returns absolute paths; a relative path would resolve against the daemon's working directory")
	}

	abs, err := filepath.Abs(p)
	if err != nil {
		return "", errs.Wrap(errs.Internal, err, "resolving path %q", p)
	}
	return abs, nil
}
