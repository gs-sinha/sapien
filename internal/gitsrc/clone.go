package gitsrc

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/gs-sinha/sapien/internal/errs"
)

// CloneInto clones url into dir as an ordinary working checkout -- no
// sparse-checkout and no sapien.json, unlike the managed cache clones
// Ensure creates. dir must be new or an existing empty directory, so a
// clone can never overwrite work that is already there. It is what
// POST /v1/workspaces/clone uses to open a repository the user picked.
func (m *Manager) CloneInto(ctx context.Context, url, dir string) error {
	if url == "" {
		return errs.New(errs.Invalid, "clone url is required")
	}
	// A url starting with "-" would be parsed as a git option (option
	// injection), and IsGitURL's scp-like pattern does not exclude it. The
	// "--" below ends option parsing as a second line of defense.
	if strings.HasPrefix(url, "-") {
		return errs.New(errs.Invalid, "clone url must not start with %q", "-").
			WithDetail("url", url)
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "resolving clone directory %q", dir)
	}

	if info, statErr := os.Stat(abs); statErr == nil {
		if !info.IsDir() {
			return errs.New(errs.Invalid, "clone destination %s is not a directory", abs).
				WithDetail("dir", abs)
		}
		entries, readErr := os.ReadDir(abs)
		if readErr != nil {
			return errs.Wrap(errs.Internal, readErr, "reading clone destination %s", abs)
		}
		if len(entries) > 0 {
			return errs.New(errs.Conflict, "clone destination %s is not empty", abs).
				WithDetail("dir", abs).
				WithHint("choose a new or empty directory for the clone")
		}
	} else if !os.IsNotExist(statErr) {
		return errs.Wrap(errs.Internal, statErr, "checking clone destination %s", abs)
	}

	if _, err := m.runTimeout(ctx, m.cloneTimeout, "", "clone", "--", url, abs); err != nil {
		return withDetail(err, "url", url)
	}
	return nil
}

// InitRepo runs `git init` in dir, turning an existing directory into a
// repository. It is the optional git-init on POST /v1/workspaces/create.
func (m *Manager) InitRepo(ctx context.Context, dir string) error {
	_, err := m.runTimeout(ctx, m.timeout, dir, "init")
	return err
}
