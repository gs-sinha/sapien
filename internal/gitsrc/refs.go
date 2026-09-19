package gitsrc

import (
	"context"
	"sort"
	"strings"
)

// RemoteRefs is Manager.LsRemote's answer (PLAN §34f item 2): every branch
// and tag a remote advertises, and which branch it treats as default.
type RemoteRefs struct {
	Default  string
	Branches []string
	Tags     []string
}

// LsRemote lists url's branches and tags with the Manager's own timeout and
// GIT_TERMINAL_PROMPT=0 (via run, like every other git invocation in this
// package): `ls-remote --symref <url> HEAD` for the default branch (the
// same query resolveDefaultRef falls back to), then `ls-remote --heads
// --tags <url>` for the rest. An annotated tag's dereferenced "^{}" peeled
// line is dropped -- the tag's own refs/tags/<tag> line already names it,
// so keeping both would list it twice. Branches and Tags are sorted, with
// Default moved to the front of Branches when it is one of them.
func (m *Manager) LsRemote(ctx context.Context, url string) (*RemoteRefs, error) {
	refs := &RemoteRefs{}

	if out, err := m.run(ctx, "", "ls-remote", "--symref", "--", url, "HEAD"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if !strings.HasPrefix(line, "ref:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				refs.Default = strings.TrimPrefix(fields[1], "refs/heads/")
			}
			break
		}
	}

	out, err := m.run(ctx, "", "ls-remote", "--heads", "--tags", "--", url)
	if err != nil {
		return nil, withDetail(err, "url", url)
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := fields[1]
		if strings.HasSuffix(name, "^{}") {
			// The peeled commit a tag object points at; the tag's own
			// refs/tags/<tag> line above already named it.
			continue
		}
		switch {
		case strings.HasPrefix(name, "refs/heads/"):
			refs.Branches = append(refs.Branches, strings.TrimPrefix(name, "refs/heads/"))
		case strings.HasPrefix(name, "refs/tags/"):
			refs.Tags = append(refs.Tags, strings.TrimPrefix(name, "refs/tags/"))
		}
	}

	sort.Strings(refs.Branches)
	sort.Strings(refs.Tags)
	if refs.Default != "" {
		for i, b := range refs.Branches {
			if b == refs.Default {
				refs.Branches = append(refs.Branches[:i:i], refs.Branches[i+1:]...)
				refs.Branches = append([]string{refs.Default}, refs.Branches...)
				break
			}
		}
	}
	return refs, nil
}

// RefExists reports whether ref names a branch or tag url's remote has
// (`ls-remote <url> <ref>` -- a single targeted query, cheaper than a full
// LsRemote when only existence matters, e.g. SetRef validating a ref before
// writing it anywhere).
func (m *Manager) RefExists(ctx context.Context, url, ref string) (bool, error) {
	out, err := m.run(ctx, "", "ls-remote", "--heads", "--tags", "--", url, ref)
	if err != nil {
		return false, withDetail(err, "url", url)
	}
	return strings.TrimSpace(out) != "", nil
}
