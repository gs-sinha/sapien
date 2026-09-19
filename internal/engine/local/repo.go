package local

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// repoAPI implements engine.RepoAPI over a Local (PLAN §7b): the
// workspace's own git repository, whose flows/, memories/ and examples/
// are the workspace tier. Every method here defers its actual git work to
// internal/gitsrc (Manager.RepoStatus/FetchRepo/PullFastForward), which is
// what enforces "read-only except an explicit fetch or fast-forward pull".
type repoAPI struct{ l *Local }

var _ engine.RepoAPI = (*repoAPI)(nil)

// syncRepoBestEffort is Services().Sync("")'s ("Sync all") repository step
// (PLAN §7b): the workspace's own repository rides along with every
// service's sync, but a repo error must never fail "Sync all" -- the
// services list it already computed is the thing that call promised, and
// the transport layer (HTTP/CLI/MCP) calls Repo().Sync on its own besides
// for the repository's own status. Only attempted when the workspace is
// actually a git checkout, so a plain (non-git) workspace never pays for a
// query whose answer it already knows.
func (l *Local) syncRepoBestEffort(ctx context.Context) {
	status, err := l.gitMgr.RepoStatus(ctx, l.ws.Dir)
	if err != nil || !status.InGit {
		return
	}
	if _, err := l.Repo().Sync(ctx); err != nil {
		l.logger.Warn("syncing the workspace repository failed", "error", err)
	}
}

// Repo returns the workspace repository API.
func (l *Local) Repo() engine.RepoAPI { return &repoAPI{l: l} }

// Status reports the repository from refs on disk: no network, matching
// gitsrc.Manager.RepoStatus. FetchError carries the last fetch's failure
// (from the daemon's tick or an explicit Fetch/Sync), cleared by the next
// successful fetch -- see setRepoFetchError.
func (r *repoAPI) Status(ctx context.Context) (*domain.RepoStatus, error) {
	l := r.l
	status, err := l.gitMgr.RepoStatus(ctx, l.ws.Dir)
	if err != nil {
		return nil, err
	}
	status.FetchError = l.repoFetchError()
	return status, nil
}

// Fetch runs `git fetch` and returns the refreshed status; the working
// tree is never touched. Both outcomes -- success and failure -- update
// the Local's remembered fetch error (setRepoFetchError) and emit
// EventWorkspaceRepo: a failed fetch still emits a status carrying
// FetchError, so a UI can show "fetch failed: ..." instead of silence
// (there is no other signal that a periodic tick failed).
func (r *repoAPI) Fetch(ctx context.Context) (*domain.RepoStatus, error) {
	l := r.l
	status, fetchErr := l.fetchRepoStatus(ctx)
	if status == nil {
		// RepoStatus itself failed (not the fetch) -- nothing to emit.
		return nil, fetchErr
	}
	l.emit(domain.EventWorkspaceRepo, status)
	return status, fetchErr
}

// Pull fast-forwards the checkout onto its upstream after checking every
// precondition itself, then reindexes the workspace tier so the pulled
// files are visible immediately rather than waiting for the next Open or
// file-watcher event.
func (r *repoAPI) Pull(ctx context.Context) (*domain.RepoStatus, error) {
	l := r.l
	status, err := l.gitMgr.RepoStatus(ctx, l.ws.Dir)
	if err != nil {
		return nil, err
	}
	status.FetchError = l.repoFetchError()

	final, err := l.pullRepo(ctx, status)
	if err != nil {
		return nil, err
	}
	l.emit(domain.EventWorkspaceRepo, final)
	return final, nil
}

// Sync is "Sync all"'s repository step: Fetch, then the same pull decision
// Pull makes, but reported through Skipped instead of an error -- a pull
// that cannot proceed is the normal case for "Sync all" (not a git
// repository at all, a developer with uncommitted work, a branch nobody
// has pushed yet), not a failure of the operation. Only a fetch failure, or
// a git failure Pull would not classify as one of those ordinary reasons,
// is returned as an error.
func (r *repoAPI) Sync(ctx context.Context) (*domain.RepoStatus, error) {
	l := r.l
	status, err := l.fetchRepoStatus(ctx)
	if err != nil {
		if status != nil {
			l.emit(domain.EventWorkspaceRepo, status)
		}
		return status, err
	}

	if !status.InGit {
		status.Skipped = "not a git repository"
		l.emit(domain.EventWorkspaceRepo, status)
		return status, nil
	}

	final, pullErr := l.pullRepo(ctx, status)
	if pullErr != nil {
		if errs.CodeOf(pullErr) != errs.Conflict {
			// pullRepo's own InGit check cannot fire (status.InGit is
			// already true above), so the only conflicts left are the
			// ordinary reasons below; anything else is a real git failure
			// -- Sync's contract only swallows the ordinary ones.
			return nil, pullErr
		}
		switch {
		case status.Dirty > 0:
			status.Skipped = fmt.Sprintf("uncommitted changes (%d files)", status.Dirty)
		case status.Upstream == "":
			status.Skipped = "no upstream"
		default:
			status.Skipped = "diverged"
		}
		l.emit(domain.EventWorkspaceRepo, status)
		return status, nil
	}

	l.emit(domain.EventWorkspaceRepo, final)
	return final, nil
}

// fetchRepoStatus runs FetchRepo -- unless the workspace isn't a git
// repository at all, in which case there is nothing to fetch and recording
// one as a "failed fetch" would stick in repoFetchErr forever, since a
// non-git workspace can never produce the successful fetch that clears it
// -- and returns the refreshed status either way (nil only when the status
// query itself, not the fetch, fails). Shared by Fetch and Sync so each
// emits exactly once, at its own call site, rather than here.
func (l *Local) fetchRepoStatus(ctx context.Context) (*domain.RepoStatus, error) {
	status, err := l.gitMgr.RepoStatus(ctx, l.ws.Dir)
	if err != nil {
		return nil, err
	}
	if !status.InGit {
		status.FetchError = l.repoFetchError()
		return status, nil
	}

	fetchErr := l.gitMgr.FetchRepo(ctx, l.ws.Dir)
	l.setRepoFetchError(fetchErr)

	status, err = l.gitMgr.RepoStatus(ctx, l.ws.Dir)
	if err != nil {
		return nil, err
	}
	status.FetchError = l.repoFetchError()
	if fetchErr != nil {
		return status, fetchErr
	}
	return status, nil
}

// pullRepo applies Pull's preconditions against an already-computed
// status (Fetch's status inside Sync, or a fresh one from Pull itself),
// then runs the fast-forward and the workspace-tier reindex. It never
// emits; callers own that. Returning status unchanged (Pulled left false)
// for Behind == 0 is deliberate -- "nothing to do" is success, not a
// refusal, so it carries no error and no Skipped reason.
func (l *Local) pullRepo(ctx context.Context, status *domain.RepoStatus) (*domain.RepoStatus, error) {
	if !status.InGit {
		return nil, errs.New(errs.Invalid, "not a git repository")
	}
	if status.Dirty > 0 {
		return nil, errs.New(errs.Conflict, "uncommitted changes (%d files); commit or stash", status.Dirty).
			WithDetail("dirty", status.Dirty).
			WithHint("commit or stash your changes in the workspace repository, then pull again")
	}
	if status.Upstream == "" {
		return nil, errs.New(errs.Conflict, "branch has no upstream")
	}
	if status.Behind == 0 {
		return status, nil
	}

	count, err := l.gitMgr.PullFastForward(ctx, l.ws.Dir)
	if err != nil {
		return nil, err
	}

	// Best effort, like reindexKnowledgeAreas at Open: a reindex failure
	// here should never turn a successful pull into a reported failure --
	// at worst the next reindex trigger (another Open, a watcher event)
	// catches up.
	if err := l.reindexWorkspaceFlows(ctx); err != nil {
		l.logger.Warn("reindexing workspace flows after a repository pull failed", "error", err)
	}
	if _, err := l.memStore.Reindex(ctx); err != nil {
		l.logger.Warn("reindexing memories after a repository pull failed", "error", err)
	}
	if _, err := l.exStore.Reindex(ctx); err != nil {
		l.logger.Warn("reindexing examples after a repository pull failed", "error", err)
	}

	final, err := l.gitMgr.RepoStatus(ctx, l.ws.Dir)
	if err != nil {
		return nil, err
	}
	final.FetchError = l.repoFetchError()
	final.Pulled = true
	final.PulledCount = count
	return final, nil
}

// repoFetchError and setRepoFetchError guard Local.repoFetchErr (declared
// in local.go): the last workspace-repository fetch's failure message, or
// "" once a fetch has succeeded since. Read by Status/Fetch/Sync's
// returned RepoStatus.FetchError; written by every fetch, whether it came
// from an explicit Fetch/Sync call or the daemon's periodic tick
// (watch.go's fetchRepoPeriodically), which is why it needs the mutex --
// those two can race.
func (l *Local) repoFetchError() string {
	l.repoFetchMu.Lock()
	defer l.repoFetchMu.Unlock()
	return l.repoFetchErr
}

func (l *Local) setRepoFetchError(err error) {
	l.repoFetchMu.Lock()
	defer l.repoFetchMu.Unlock()
	if err != nil {
		l.repoFetchErr = err.Error()
		return
	}
	l.repoFetchErr = ""
}

// Push sends the workspace repository's unpushed commits to its upstream
// (PLAN §7b): the one place Sapien pushes anywhere, and only on request,
// only this repository. Refused (errs.Invalid) when the workspace is not a
// git repository; refused (errs.Conflict) when the branch is behind --
// gitsrc.PushRepo never forces, so a behind branch must be pulled first,
// same message the UI shows; a no-op success (status unchanged, no git
// call at all) when there is nothing ahead. Otherwise gitsrc.PushRepo runs
// the push, a fresh Status carries Pushed/PushedCount, and
// EventWorkspaceRepo is emitted, mirroring Fetch/Pull/Sync. A push failure
// (an auth problem, a diverged remote someone force-pushed) is returned as
// gitsrc's own classified error, whose hint already covers the common
// cases.
func (r *repoAPI) Push(ctx context.Context) (*domain.RepoStatus, error) {
	l := r.l
	status, err := l.gitMgr.RepoStatus(ctx, l.ws.Dir)
	if err != nil {
		return nil, err
	}
	status.FetchError = l.repoFetchError()

	if !status.InGit {
		return nil, errs.New(errs.Invalid, "not a git repository")
	}
	if status.Behind > 0 {
		return nil, errs.New(errs.Conflict, "branch is behind its upstream by %d commits; pull first", status.Behind).
			WithDetail("behind", status.Behind)
	}
	if status.Ahead == 0 {
		return status, nil
	}

	count, err := l.gitMgr.PushRepo(ctx, l.ws.Dir)
	if err != nil {
		return nil, err
	}

	final, err := l.gitMgr.RepoStatus(ctx, l.ws.Dir)
	if err != nil {
		return nil, err
	}
	final.FetchError = l.repoFetchError()
	final.Pushed = true
	final.PushedCount = count
	l.emit(domain.EventWorkspaceRepo, final)
	return final, nil
}

// Changes lists every changed file the workspace repository knows about
// (PLAN §34f item 1): gitsrc.Manager.Changes for the raw list -- one
// `status` plus, when there is an upstream, one `log` -- classified into a
// kind/id/title by buildChangeIndex, plus one entry per registered service
// (serviceChanges). A workspace that is not a git repository at all comes
// back with an empty Files/Services, not an error: Changes is a read, and
// "nothing to show" is what a plain (non-git) workspace's Changes page
// should say.
func (r *repoAPI) Changes(ctx context.Context) (*domain.RepoChanges, error) {
	l := r.l
	status, err := r.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := &domain.RepoChanges{Status: *status, Files: []domain.RepoFileChange{}}
	if !status.InGit {
		return out, nil
	}

	raw, err := l.gitMgr.Changes(ctx, l.ws.Dir)
	if err != nil {
		return nil, err
	}

	// wsRelPrefix is l.ws.Dir's own repo-root-relative path (with a
	// trailing "/"), "" when the workspace directory is the repository
	// root itself -- the case that matters everywhere except a monorepo
	// workspace. Stripping it from a change's Path is what lets
	// classifyPath recognize sapien.workspace.yaml, .gitignore and
	// environments/ by their workspace-relative spelling even when the
	// repository holds more than this one workspace.
	wsRelPrefix := ""
	if rel, ok := repoRelative(status.Root, resolvedOrSelf(l.ws.Dir)); ok && rel != "" {
		wsRelPrefix = rel + "/"
	}

	idx := l.buildChangeIndex(ctx, status.Root)
	for i := range raw {
		classifyPath(&raw[i], idx, strings.TrimPrefix(raw[i].Path, wsRelPrefix))
	}
	out.Files = raw
	out.Services = r.serviceChanges(ctx)
	return out, nil
}

// serviceChanges builds the Changes page's services array (PLAN §34f item
// 1): a service bound to a local checkout (Source.Kind local, whether from
// an override or a native local source) reports its path, branch, dirty
// count and changed files under its API package directory -- a read-only
// gitsrc.Changes scoped to that directory, never a fetch, commit or
// checkout there; a team-sourced service reports its effective ref
// (Source.Ref, already reflecting any local ref override -- PLAN §34f item
// 2) and no files, since a managed clone is not the developer's own work.
// Any one service's lookup failing (an unresolvable local path, a git
// query erroring) drops only that service's detail, never the whole list.
func (r *repoAPI) serviceChanges(ctx context.Context) []domain.RepoServiceChanges {
	l := r.l
	out := make([]domain.RepoServiceChanges, 0, len(l.ws.Services))
	for _, ref := range l.ws.Services {
		if ref.Source.Kind != domain.SourceLocal {
			out = append(out, domain.RepoServiceChanges{
				Name: ref.Name,
				Mode: domain.BindingTeam,
				Ref:  ref.Source.Ref,
			})
			continue
		}

		sc := domain.RepoServiceChanges{Name: ref.Name, Mode: domain.BindingLocal}
		root, err := workspace.ResolveSourcePath(l.ws, ref.Source)
		if err != nil {
			out = append(out, sc)
			continue
		}
		sc.Path = root
		if co, err := l.gitMgr.Describe(ctx, root); err == nil {
			sc.Branch = co.Branch
			sc.Dirty = co.Dirty > 0
		}
		if svc, err := l.cat.GetService(ctx, ref.Name); err == nil && svc != nil && svc.PackageDir != "" {
			if files, ferr := l.gitMgr.Changes(ctx, svc.PackageDir); ferr == nil {
				sc.Files = files
			}
		}
		out = append(out, sc)
	}
	return out
}

// catalogPathEntry is one workspace-tier item buildChangeIndex indexes by
// its repo-root-relative path.
type catalogPathEntry struct {
	kind, id, title string
}

// buildChangeIndex indexes every workspace-tier flow, memory and example by
// its repo-root-relative path under root (PLAN §34f item 1's Changes
// classification). A lookup failure for any one kind degrades to no
// entries for that kind rather than failing Changes outright -- a listing
// must never fail because one store's List did.
func (l *Local) buildChangeIndex(ctx context.Context, root string) map[string]catalogPathEntry {
	idx := map[string]catalogPathEntry{}

	if flows, err := l.cat.ListFlows(ctx, domain.FlowOwnerWorkspace, ""); err == nil {
		for _, fs := range flows {
			rel, ok := repoRelative(root, fs.Path)
			if !ok {
				continue
			}
			title := fs.Name
			if title == "" {
				title = fs.ID
			}
			idx[rel] = catalogPathEntry{kind: domain.RepoKindFlow, id: fs.ID, title: title}
		}
	}

	if mems, err := l.memStore.List(ctx, domain.MemoryQuery{}); err == nil {
		for _, m := range mems {
			if m.Tier != domain.TierWorkspace || m.FilePath == "" {
				continue
			}
			rel, ok := repoRelative(root, m.FilePath)
			if !ok {
				continue
			}
			idx[rel] = catalogPathEntry{kind: domain.RepoKindMemory, id: m.ID, title: memoryTitle(m)}
		}
	}

	if exs, err := l.exStore.List(ctx, domain.ExampleQuery{}); err == nil {
		for _, ex := range exs {
			if ex.Tier != domain.TierWorkspace || ex.Path == "" {
				continue
			}
			rel, ok := repoRelative(root, ex.Path)
			if !ok {
				continue
			}
			title := ex.Description
			if title == "" {
				title = ex.ID
			}
			idx[rel] = catalogPathEntry{kind: domain.RepoKindExample, id: ex.ID, title: title}
		}
	}

	return idx
}

// repoRelative resolves abs (an absolute file path) to a repo-root-relative,
// "/"-separated path under root, or ok=false when it is not under root at
// all. Both sides are compared symlink-resolved (resolvedOrSelf), matching
// what git's own output (gitMgr.Changes, via root's Toplevel) is already
// relative to -- see gitsrc/describe_test.go's TestManager_Toplevel for why
// that matters on a symlinked temp or home directory.
func repoRelative(root, abs string) (string, bool) {
	rel, err := filepath.Rel(resolvedOrSelf(root), resolvedOrSelf(abs))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// memoryTitle derives a short label for a memory, which has no Name field
// of its own: the first non-empty line of its Markdown body, a leading
// heading marker trimmed and length capped, falling back to its ID when
// the body is empty.
func memoryTitle(m domain.Memory) string {
	for _, line := range strings.Split(m.Text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line == "" {
			continue
		}
		if len(line) > 80 {
			line = line[:80]
		}
		return line
	}
	return m.ID
}

// classifyPath fills Kind/ID/Title on c in place: a flow/memory/example
// match from idx (keyed by c.Path, repo-root-relative) first; otherwise
// workspaceRel -- c.Path with the workspace directory's own repo-relative
// prefix stripped, so this recognizes sapien.workspace.yaml, .gitignore and
// environments/ by their workspace-relative spelling even inside a
// monorepo -- against the well-known workspace files; RepoKindOther
// otherwise.
func classifyPath(c *domain.RepoFileChange, idx map[string]catalogPathEntry, workspaceRel string) {
	if entry, ok := idx[c.Path]; ok {
		c.Kind, c.ID, c.Title = entry.kind, entry.id, entry.title
		return
	}
	switch {
	case workspaceRel == domain.WorkspaceFileName, workspaceRel == ".gitignore":
		c.Kind = domain.RepoKindWorkspace
	case strings.HasPrefix(workspaceRel, domain.EnvironmentsDir+"/"):
		c.Kind = domain.RepoKindEnvironment
	default:
		c.Kind = domain.RepoKindOther
	}
}

// Diff reports one file's diff or (for an untracked file) content
// (PLAN §34f item 1). path is repo-root-relative, as Changes reports it;
// gitsrc.Manager.Diff enforces the path safety rule (cleaned, no absolute
// or escaping path, refuses an ignored file) and does the actual git work.
func (r *repoAPI) Diff(ctx context.Context, path string) (*domain.RepoDiff, error) {
	return r.l.gitMgr.Diff(ctx, r.l.ws.Dir, path)
}

// Commit stages and commits exactly paths (repo-root-relative, as Changes
// reports them) in the workspace repository with one commit (PLAN §34f item
// 1): never pushes, never amends, never passes --no-verify (gitsrc.
// CommitPaths). Each path is validated with the same rule Diff applies
// (gitsrc.Manager.SafePath: cleaned, no absolute or escaping path, refused
// if git ignores it) before anything is staged. message is required
// (CommitPaths trims and checks it); paths must contain at least one
// changed file (CommitPaths refuses otherwise). Emits EventWorkspaceRepo
// with the refreshed status so the status bar (and, on the next read, every
// flow/memory/example listing's Shipped state -- computed live from git,
// never cached) reflects the commit immediately.
func (r *repoAPI) Commit(ctx context.Context, paths []string, message string) (*domain.RepoCommitResult, error) {
	l := r.l
	if len(paths) == 0 {
		return nil, errs.New(errs.Invalid, "no paths given")
	}

	abs := make([]string, 0, len(paths))
	committed := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, a, err := l.gitMgr.SafePath(ctx, l.ws.Dir, p)
		if err != nil {
			return nil, err
		}
		abs = append(abs, a)
		committed = append(committed, rel)
	}

	sha, err := l.gitMgr.CommitPaths(ctx, l.ws.Dir, abs, message)
	if err != nil {
		return nil, err
	}

	status, err := r.Status(ctx)
	if err != nil {
		return nil, err
	}
	l.emit(domain.EventWorkspaceRepo, status)

	return &domain.RepoCommitResult{Commit: sha, Committed: committed, Status: *status}, nil
}
