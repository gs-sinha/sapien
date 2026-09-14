package local

import (
	"context"
	"fmt"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
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

// Push: Phase 0 stub, filled in by the repository work.
func (r *repoAPI) Push(ctx context.Context) (*domain.RepoStatus, error) {
	return nil, errs.New(errs.NotImplemented, "pushing the workspace repository is not available yet")
}
