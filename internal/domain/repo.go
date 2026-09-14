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
}
