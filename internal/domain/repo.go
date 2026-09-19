package domain

import "time"

// RepoStatus describes the workspace's own git repository: the one the
// team clones, whose flows/, memories/ and examples/ are the workspace
// tier (PLAN §7b). Every field comes from read-only git queries against
// refs already on disk; Behind and Ahead are as of the last fetch, which
// the daemon runs on its git tick and `Sync all` runs on request.
type RepoStatus struct {
	// InGit is false when the workspace directory is not inside a git
	// repository; every other field is then zero.
	InGit    bool   `json:"in_git"`
	Root     string `json:"root,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Remote   string `json:"remote,omitempty"`   // origin URL
	Upstream string `json:"upstream,omitempty"` // e.g. origin/main; "" when the branch tracks nothing
	// Behind counts upstream commits this checkout lacks; Ahead counts
	// local commits the upstream lacks. Both zero without an upstream.
	Behind int `json:"behind"`
	Ahead  int `json:"ahead"`
	// Dirty counts modified and untracked files: what blocks a pull.
	Dirty int `json:"dirty"`
	// FetchedAt is when the remote was last fetched (FETCH_HEAD's mtime);
	// zero when never.
	FetchedAt time.Time `json:"fetched_at,omitzero"`
	// FetchError is the last fetch's failure, when it failed; cleared by
	// the next successful fetch.
	FetchError string `json:"fetch_error,omitempty"`
	// Pulled reports that the operation that produced this status
	// fast-forwarded the checkout, with the commits it brought in.
	Pulled      bool `json:"pulled,omitempty"`
	PulledCount int  `json:"pulled_count,omitempty"`
	// Skipped says why a sync did not pull: dirty files, no upstream, a
	// diverged branch. Empty when nothing was skipped.
	Skipped string `json:"skipped,omitempty"`
	// Pushed reports that the operation that produced this status pushed
	// the branch, with how many commits went up.
	Pushed      bool `json:"pushed,omitempty"`
	PushedCount int  `json:"pushed_count,omitempty"`
}

// Changes states (PLAN §34f item 1): what the Changes page's file tree and
// `sapien workspace changes` group by. Distinct from the Ship* states above
// -- those describe one workspace-tier file's promotion journey; these
// describe what `git status` (plus unpushed commits) says about any file in
// the whole workspace repository.
const (
	ChangeUntracked  = "untracked"
	ChangeModified   = "modified"
	ChangeDeleted    = "deleted"
	ChangeRenamed    = "renamed"
	ChangeConflicted = "conflicted"
	ChangeUnpushed   = "unpushed"
)

// Kinds for RepoFileChange.Kind (PLAN §34f item 1): what a changed path is,
// from matching it against the catalog's known file paths for the
// workspace tier, or its own well-known location otherwise.
const (
	RepoKindFlow        = "flow"
	RepoKindMemory      = "memory"
	RepoKindExample     = "example"
	RepoKindEnvironment = "environment"
	RepoKindWorkspace   = "workspace"
	RepoKindOther       = "other"
)

// RepoFileChange is one file the workspace's own git repository reports
// changed (PLAN §34f item 1): from `git status`, or from an unpushed commit
// touching an otherwise-clean file. Path (and OldPath, for a rename) are
// repo-root-relative, "/"-separated, regardless of where in the repository
// the workspace directory sits. Kind/ID/Title are filled in by the engine
// layer (gitsrc knows nothing about flows, memories or examples); empty for
// a file the catalog does not own (kind "workspace", "environment" or
// "other").
type RepoFileChange struct {
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"` // renames only: the path before
	State   string `json:"state"`              // one of the Change* constants above
	Kind    string `json:"kind,omitempty"`     // flow|memory|example|environment|workspace|other
	ID      string `json:"id,omitempty"`
	Title   string `json:"title,omitempty"`
}

// RepoServiceChanges is one workspace service's entry on the Changes page
// (PLAN §34f item 1): a bound local checkout's branch and changed files
// under its API package (read-only -- Sapien never fetches, commits or
// checks out there), or a team-sourced service's effective ref with no
// files, since its managed clone is not a developer's work to show changes
// for.
type RepoServiceChanges struct {
	Name   string           `json:"name"`
	Mode   string           `json:"mode"` // domain.BindingLocal | domain.BindingTeam
	Path   string           `json:"path,omitempty"`
	Branch string           `json:"branch,omitempty"`
	Ref    string           `json:"ref,omitempty"`
	Dirty  bool             `json:"dirty"`
	Files  []RepoFileChange `json:"files,omitempty"`
}

// RepoChanges is engine.RepoAPI.Changes' answer (PLAN §34f item 1): the
// workspace repository's status alongside every changed file it or a bound
// service checkout knows about.
type RepoChanges struct {
	Status   RepoStatus           `json:"status"`
	Files    []RepoFileChange     `json:"files"`
	Services []RepoServiceChanges `json:"services,omitempty"`
}

// RepoDiff is engine.RepoAPI.Diff's answer (PLAN §34f item 1): a tracked
// file's diff against HEAD (or, for a clean-but-unpushed file, against its
// upstream), or an untracked file's raw content. Binary and Truncated are
// mutually informative, not exclusive: a binary file never carries Diff or
// Content; a large text file's Diff or Content is capped with Truncated set.
type RepoDiff struct {
	Path      string `json:"path"`
	State     string `json:"state,omitempty"`
	Diff      string `json:"diff,omitempty"`
	Content   string `json:"content,omitempty"` // untracked files only
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}

// RepoCommitResult is engine.RepoAPI.Commit's answer (PLAN §34f item 1).
type RepoCommitResult struct {
	Commit    string     `json:"commit"`
	Committed []string   `json:"committed"` // the paths actually committed, repo-root-relative
	Status    RepoStatus `json:"status"`
}
