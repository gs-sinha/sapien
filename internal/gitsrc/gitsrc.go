// Package gitsrc manages Sapien's local clones of git-backed service
// packages (PLAN.md §18). Sapien never bundles or implements git itself: it
// shells out to the system `git` binary so the user's SSH agent and
// credential helpers apply unmodified and Sapien stores no credentials
// (PLAN §2.2, §18, §28).
//
// A Manager keeps one sparse, blobless clone per remote URL under its cache
// directory (default "~/.sapien/repos"), checked out at a resolved ref
// (branch, tag, or commit). Ensure creates that clone on first use and
// simply resolves it thereafter; Sync fetches and updates it to the
// configured ref, reporting whether the resolved commit moved.
package gitsrc

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // content-addressing a URL for a cache directory name, not a security use.
	"encoding/hex"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/errs"
)

// defaultTimeout is the per-git-invocation budget used when Options.Timeout
// is not set.
const defaultTimeout = 60 * time.Second

// defaultSubdir is the package directory used when a Source does not
// declare one (PLAN §7, §18).
const defaultSubdir = "api"

// Options configures a Manager. Every field is optional.
type Options struct {
	// CacheDir is where managed clones live. Default: "~/.sapien/repos"
	// (or "<TempDir>/.sapien/repos" if the home directory cannot be
	// determined).
	CacheDir string
	// Git is the git executable to run. Default: "git" (resolved via PATH).
	Git string
	// Timeout bounds every individual git invocation. Default: 60s.
	Timeout time.Duration
	// Logger receives debug-level records of every git command run. Default:
	// slog.Default().
	Logger *slog.Logger
	// Env is extra environment passed to every git invocation, in
	// addition to the process's own environment. GIT_TERMINAL_PROMPT=0 is
	// always forced (never overridable) so a missing credential never
	// blocks waiting for interactive input.
	Env []string
}

// Manager creates and maintains managed git clones for git-sourced services.
type Manager struct {
	cacheDir string
	git      string
	timeout  time.Duration
	logger   *slog.Logger
	env      []string
}

// New builds a Manager from opts, applying defaults for every unset field.
func New(opts Options) *Manager {
	cacheDir := opts.CacheDir
	if cacheDir == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			cacheDir = filepath.Join(home, ".sapien", "repos")
		} else {
			cacheDir = filepath.Join(os.TempDir(), ".sapien", "repos")
		}
	}

	gitBin := opts.Git
	if gitBin == "" {
		gitBin = "git"
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Manager{
		cacheDir: cacheDir,
		git:      gitBin,
		timeout:  timeout,
		logger:   logger,
		env:      append([]string(nil), opts.Env...),
	}
}

// Dir returns the managed clone directory for url: a stable path derived
// from a content hash of url plus a human-readable slug of its repository
// name, so cache directories are both collision-resistant and legible in a
// directory listing. It does not touch the filesystem or require url to
// exist; the same url always yields the same Dir.
func (m *Manager) Dir(url string) string {
	sum := sha1.Sum([]byte(url)) //nolint:gosec // see the import comment above.
	hash := hex.EncodeToString(sum[:])[:16]

	slug := repoSlug(url)
	if slug == "" {
		slug = "repo"
	}

	return filepath.Join(m.cacheDir, hash+"-"+slug)
}

// scpLikeGitURL matches the scp-like git remote syntax "[user@]host:path"
// (e.g. "git@github.com:org/repo.git"), which carries no URL scheme.
var scpLikeGitURL = regexp.MustCompile(`^[A-Za-z0-9_.-]+@[A-Za-z0-9_.-]+:`)

// gitURLSchemes are the URL schemes IsGitURL recognizes.
var gitURLSchemes = []string{"http://", "https://", "ssh://", "git://", "file://"}

// IsGitURL reports whether s looks like a git remote URL: an explicit
// scheme (http(s)://, ssh://, git://, file://) or the scp-like
// "user@host:path" form. A plain filesystem path -- absolute, relative, or
// "~"-prefixed, with no scheme and no "user@host:" prefix -- is not a git
// URL, even if it happens to contain a colon (e.g. a Windows drive letter).
func IsGitURL(s string) bool {
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	for _, scheme := range gitURLSchemes {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return scpLikeGitURL.MatchString(s)
}

// repoSlug extracts a filesystem-friendly slug from a git URL's repository
// name: the last path segment, with any URL scheme or scp-like "user@host:"
// prefix and a trailing ".git" stripped first. It returns "" for input it
// cannot make sense of.
func repoSlug(rawURL string) string {
	s := rawURL

	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	} else if idx := strings.Index(s, ":"); idx >= 0 && !strings.Contains(s[:idx], "/") {
		// scp-like syntax: "[user@]host:path".
		s = s[idx+1:]
	}

	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")

	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}

	return slugify(s)
}

// slugify lower-cases s and collapses runs of non [a-z0-9] characters into a
// single '-', trimming any trailing '-'. It returns "" for a blank or
// all-punctuation input.
func slugify(s string) string {
	var b strings.Builder
	prevDash := true // avoid a leading dash
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// run executes git with args in dir (the process's own working directory
// when dir is ""), applying m.timeout, capturing stdout/stderr, and forcing
// GIT_TERMINAL_PROMPT=0. An already-done ctx is rejected immediately,
// without starting a process. A failure is returned as errs.Cancelled (ctx
// cancelled or deadline exceeded) or errs.ServiceSource (any other git
// failure, stderr attached as a detail and a hint attached for common
// failure patterns); callers add a "url" detail where they have one.
func (m *Manager) run(ctx context.Context, dir string, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", errs.Wrap(errs.Cancelled, err, "git %s: cancelled", strings.Join(args, " "))
	}

	cctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, m.git, args...) //nolint:gosec // m.git and args are operator-controlled, not user input.
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = buildEnv(m.env)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if m.logger != nil {
		m.logger.Debug("gitsrc: running git", "args", args, "dir", dir)
	}

	if err := cmd.Run(); err != nil {
		return stdout.String(), gitError(cctx, args, stderr.String(), err)
	}
	return stdout.String(), nil
}

// buildEnv merges the process environment with extra, then forces
// GIT_TERMINAL_PROMPT=0 so a missing credential fails instead of hanging.
// Later sources win on key collisions; duplicate keys are never emitted
// (some platforms resolve duplicate env entries inconsistently).
func buildEnv(extra []string) []string {
	merged := map[string]string{}
	apply := func(list []string) {
		for _, kv := range list {
			if i := strings.IndexByte(kv, '='); i >= 0 {
				merged[kv[:i]] = kv[i+1:]
			}
		}
	}
	apply(os.Environ())
	apply(extra)
	merged["GIT_TERMINAL_PROMPT"] = "0"

	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}

// gitError classifies a failed git invocation.
func gitError(cctx context.Context, args []string, stderrOutput string, cause error) error {
	if cctx.Err() != nil {
		return errs.Wrap(errs.Cancelled, cctx.Err(), "git %s: cancelled", strings.Join(args, " "))
	}

	e := errs.Wrap(errs.ServiceSource, cause, "git %s failed", strings.Join(args, " ")).
		WithDetail("stderr", strings.TrimSpace(stderrOutput))
	if hint := hintFor(stderrOutput); hint != "" {
		e = e.WithHint(hint)
	}
	return e
}

// hintFor maps common git stderr patterns to an actionable hint. It returns
// "" when nothing recognizable matched.
func hintFor(stderrOutput string) string {
	s := strings.ToLower(stderrOutput)
	switch {
	case strings.Contains(s, "please tell me who you are"),
		strings.Contains(s, "author identity unknown"),
		strings.Contains(s, "unable to auto-detect email address"):
		return `git has no identity to commit as: run git config --global user.name "Your Name" and git config --global user.email you@example.com`
	case strings.Contains(s, "permission denied (publickey)"),
		strings.Contains(s, "authentication failed"),
		strings.Contains(s, "could not read username"),
		strings.Contains(s, "could not read password"),
		strings.Contains(s, "terminal prompts disabled"):
		return "check your SSH agent or credential helper; Sapien stores no credentials"
	case strings.Contains(s, "did not match any file(s) known to git"),
		strings.Contains(s, "unknown revision or path not in the working tree"),
		(strings.Contains(s, "pathspec") && strings.Contains(s, "did not match")):
		return "unknown ref: check the branch, tag, or commit configured for this service"
	case strings.Contains(s, "repository not found"),
		strings.Contains(s, "could not read from remote repository"),
		strings.Contains(s, "does not exist"),
		strings.Contains(s, "no such file or directory"):
		return "check the repository URL and that you have access to it"
	case strings.Contains(s, "not a git repository"):
		return "not a git repository"
	}
	return ""
}

// withDetail attaches a detail to err's *errs.Error form and returns it,
// or nil if err is nil.
func withDetail(err error, key string, value any) error {
	if err == nil {
		return nil
	}
	return errs.As(err).WithDetail(key, value)
}
