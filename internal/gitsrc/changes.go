package gitsrc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// diffCap bounds how much of a diff or an untracked file's content Diff
// returns (PLAN §34f item 1): large enough to read comfortably, small
// enough that a giant generated file or a huge diff never blows up a JSON
// response.
const diffCap = 256 * 1024

// Changes reports every changed file the workspace's own git repository
// knows about, repo-root-relative and "/"-separated regardless of where in
// the repository dir sits (PLAN §34f item 1): the source-control-panel view
// behind the Changes page and `sapien workspace changes`.
//
// It runs `git status --porcelain=v1 -z -uall` at the repository root --
// not dir -- so every path comes back repo-root-relative for free and dir
// only has to filter, never rewrite, a path; -z means paths are raw bytes,
// never C-quoted, and a rename arrives as two NUL-terminated fields (new
// path, then old path). When dir is a subdirectory of the repository (a
// monorepo workspace, or a bound service checkout's package directory),
// only files under dir are returned, still spelled relative to the
// repository root dir itself sits in.
//
// A file touched by a commit nobody has pushed yet, but otherwise clean, is
// reported ChangeUnpushed (`git log --name-only @{upstream}..HEAD`) -- only
// when the branch has an upstream at all; without one, Changes says nothing
// about files git status does not already mention, matching FileStates'
// "nothing shared yet" reasoning without paying for a full-history scan.
//
// Results are sorted by Path for a stable order. Kind/ID/Title are always
// left empty: gitsrc knows nothing about flows, memories or examples, and
// classifying paths against the catalog is the engine layer's job.
func (m *Manager) Changes(ctx context.Context, dir string) ([]domain.RepoFileChange, error) {
	root, err := m.Toplevel(ctx, dir)
	if err != nil {
		return nil, err
	}

	// root came back symlink-resolved (git rev-parse --show-toplevel always
	// does); dir may not have (a symlinked temp or home directory, e.g.
	// macOS's /tmp -> /private/tmp -- see gitsrc/describe_test.go's
	// TestManager_Toplevel), so resolve it the same way before comparing,
	// or a subdirectory workspace would never match its own root and every
	// change would be filtered out.
	resolvedDir := resolvedOrSelfPath(dir)
	prefix := ""
	if rel, relErr := filepath.Rel(root, filepath.Clean(resolvedDir)); relErr == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		prefix = filepath.ToSlash(rel) + "/"
	}

	out, err := m.run(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}

	changes := parsePorcelainZ(out)
	seen := make(map[string]bool, len(changes))
	result := make([]domain.RepoFileChange, 0, len(changes))
	for _, c := range changes {
		if prefix != "" && !strings.HasPrefix(c.Path, prefix) {
			continue
		}
		result = append(result, c)
		seen[c.Path] = true
	}

	if _, err := m.run(ctx, root, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
		logOut, err := m.run(ctx, root, "log", "--name-only", "--format=", "@{upstream}..HEAD", "--")
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(logOut, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			p := unquoteGitPath(line)
			if seen[p] {
				continue
			}
			if prefix != "" && !strings.HasPrefix(p, prefix) {
				continue
			}
			result = append(result, domain.RepoFileChange{Path: p, State: domain.ChangeUnpushed})
			seen[p] = true
		}
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

// parsePorcelainZ parses `git status --porcelain=v1 -z`'s output into one
// domain.RepoFileChange per entry, classifying each XY code into the
// Change* state vocabulary (see changeStateFromXY) and pairing a rename's
// two NUL-terminated fields (new path, then old path) into one entry.
func parsePorcelainZ(out string) []domain.RepoFileChange {
	fields := strings.Split(out, "\x00")
	var result []domain.RepoFileChange
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if f == "" || len(f) < 3 {
			continue
		}
		xy := f[0:2]
		path := f[3:]
		state := changeStateFromXY(xy)

		change := domain.RepoFileChange{Path: path, State: state}
		if strings.ContainsAny(xy, "RC") && i+1 < len(fields) {
			i++
			change.OldPath = fields[i]
		}
		result = append(result, change)
	}
	return result
}

// changeStateFromXY maps a porcelain v1 XY status code to the Change*
// vocabulary (PLAN §34f item 1): any unmerged combination (UU, AA, DD, AU,
// UD, UA, DU) is conflicted; "??" is untracked; an X or Y of 'R' is
// renamed; an X or Y of 'D' (that is not one of the conflict combinations
// above) is deleted; everything else -- modified, added, copied, type-
// changed, and any staged/unstaged combination of those -- is modified.
func changeStateFromXY(xy string) string {
	switch {
	case strings.ContainsRune(xy, 'U'), xy == "AA", xy == "DD":
		return domain.ChangeConflicted
	case xy == "??":
		return domain.ChangeUntracked
	case strings.ContainsRune(xy, 'R'):
		return domain.ChangeRenamed
	case strings.ContainsRune(xy, 'D'):
		return domain.ChangeDeleted
	default:
		return domain.ChangeModified
	}
}

// SafePath resolves and validates path as a repo-root-relative pathspec
// inside dir's repository (PLAN §34f item 1's path safety rule): cleans it,
// refuses an absolute input or one that escapes the repository root
// (safeRepoPath), and refuses a path git itself ignores (`git check-ignore
// -q`). It returns the cleaned relative path ("/"-separated) and its
// resolved absolute path. Diff applies the same rule to its own path
// parameter inline (it already needs the repository root for its later git
// calls); the engine layer calls this directly for Commit's paths, so both
// enforce exactly the same rule.
func (m *Manager) SafePath(ctx context.Context, dir, path string) (rel, abs string, err error) {
	root, err := m.Toplevel(ctx, dir)
	if err != nil {
		return "", "", err
	}
	rel, abs, err = safeRepoPath(root, path)
	if err != nil {
		return "", "", err
	}
	if ignored, err := m.checkIgnored(ctx, root, rel); err != nil {
		return "", "", err
	} else if ignored {
		return "", "", errs.New(errs.Invalid, "path is ignored: %s", rel).WithDetail("path", rel)
	}
	return rel, abs, nil
}

// Diff reports path's diff (PLAN §34f item 1): `git diff HEAD -- path` for
// a tracked file, so both staged and unstaged edits show; a file's raw
// content for an untracked one; `git diff @{upstream}..HEAD -- path` for a
// file that is clean but only committed here, not yet pushed (so there is
// something to show at all). path is repo-root-relative, cleaned and
// checked against dir's repository root: an absolute path, one that
// escapes the root, or one git itself ignores (`git check-ignore -q`) is
// refused with errs.Invalid. The diff or content is capped at diffCap with
// Truncated set; a binary file (a NUL byte in an untracked file's first
// bytes, or `git diff --numstat` reporting "-" for both counts) carries
// neither, only Binary.
func (m *Manager) Diff(ctx context.Context, dir, path string) (*domain.RepoDiff, error) {
	root, err := m.Toplevel(ctx, dir)
	if err != nil {
		return nil, err
	}

	rel, abs, err := safeRepoPath(root, path)
	if err != nil {
		return nil, err
	}

	if ignored, err := m.checkIgnored(ctx, root, rel); err != nil {
		return nil, err
	} else if ignored {
		return nil, errs.New(errs.Invalid, "path is ignored: %s", rel).WithDetail("path", rel)
	}

	state, err := m.singleFileState(ctx, root, rel)
	if err != nil {
		return nil, err
	}

	out := &domain.RepoDiff{Path: rel}

	if state == domain.ChangeUntracked {
		out.State = domain.ChangeUntracked
		content, binary, truncated, err := readCapped(abs, diffCap)
		if err != nil {
			return nil, errs.Wrap(errs.Internal, err, "reading %s", abs)
		}
		out.Binary = binary
		out.Truncated = truncated
		if !binary {
			out.Content = string(content)
		}
		return out, nil
	}

	diffRange := []string{"HEAD"}
	if state == "" {
		// Clean and tracked: unpushed (something to show against the
		// upstream) or fully shipped (nothing to show at all -- `git diff
		// HEAD` naturally comes back empty, which is the right answer).
		if shipped, err := m.FileStates(ctx, root, []string{abs}); err == nil && shipped[abs] == domain.ShipUnpushed {
			out.State = domain.ChangeUnpushed
			diffRange = []string{"@{upstream}..HEAD"}
		}
	} else {
		out.State = state
	}

	numstat, err := m.run(ctx, root, append([]string{"diff", "--numstat"}, append(diffRange, "--", rel)...)...)
	if err != nil {
		return nil, err
	}
	if isNumstatBinary(numstat) {
		out.Binary = true
		return out, nil
	}

	diffOut, err := m.run(ctx, root, append([]string{"diff"}, append(diffRange, "--", rel)...)...)
	if err != nil {
		return nil, err
	}
	text, truncated := capString(diffOut, diffCap)
	out.Diff = text
	out.Truncated = truncated
	return out, nil
}

// singleFileState reports rel's Change* state relative to root, or "" for a
// clean, tracked file (which Diff then resolves further via FileStates).
// This is Changes' own classifier, scoped with `-- rel` to one file so a
// diff lookup costs one status call instead of a full-repository scan.
func (m *Manager) singleFileState(ctx context.Context, root, rel string) (string, error) {
	out, err := m.run(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", rel)
	if err != nil {
		return "", err
	}
	changes := parsePorcelainZ(out)
	if len(changes) == 0 {
		return "", nil
	}
	return changes[0].State, nil
}

// isNumstatBinary reports whether `git diff --numstat`'s output marks its
// one file binary: numstat prints "-\t-\t<path>" for a binary file instead
// of add/delete line counts.
func isNumstatBinary(numstat string) bool {
	fields := strings.Fields(strings.TrimSpace(numstat))
	return len(fields) >= 2 && fields[0] == "-" && fields[1] == "-"
}

// readCapped reads path's content up to maxLen bytes, reporting whether the
// file is larger than that (truncated) and whether it looks binary (a NUL
// byte within what was read -- the same heuristic git itself uses).
func readCapped(path string, maxLen int) (content []byte, binary, truncated bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, false, err
	}
	f, err := os.Open(path) //nolint:gosec // path is validated by safeRepoPath before this is ever called.
	if err != nil {
		return nil, false, false, err
	}
	defer f.Close()

	limit := maxLen
	if int64(limit) > info.Size() {
		limit = int(info.Size())
	}
	buf := make([]byte, limit)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, false, false, err
	}
	buf = buf[:n]

	return buf, bytes.IndexByte(buf, 0) >= 0, info.Size() > int64(maxLen), nil
}

// capString truncates s to maxLen bytes on a rune boundary, reporting
// whether it did.
func capString(s string, maxLen int) (string, bool) {
	if len(s) <= maxLen {
		return s, false
	}
	cut := maxLen
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// isRuneStart reports whether b is not a UTF-8 continuation byte, so
// capString never splits a multi-byte rune.
func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// safeRepoPath cleans and validates path as a repo-root-relative pathspec
// (PLAN §34f item 1's Diff path safety), refusing an absolute input or one
// that escapes root. It returns the cleaned relative path ("/"-separated)
// and its resolved absolute path.
func safeRepoPath(root, path string) (rel, abs string, err error) {
	if strings.TrimSpace(path) == "" {
		return "", "", errs.New(errs.Invalid, "path is required")
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, "/") {
		return "", "", errs.New(errs.Invalid, "path must be relative to the repository root, not absolute: %s", path).
			WithDetail("path", path)
	}

	cleaned := filepath.Clean(filepath.FromSlash(path))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", "", errs.New(errs.Invalid, "path escapes the repository: %s", path).
			WithDetail("path", path)
	}

	absPath := filepath.Join(root, cleaned)
	relCheck, err := filepath.Rel(root, absPath)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", "", errs.New(errs.Invalid, "path escapes the repository: %s", path).
			WithDetail("path", path)
	}

	return filepath.ToSlash(cleaned), absPath, nil
}

// resolvedOrSelfPath returns p with symlinks resolved, or p itself when
// that fails outright -- used only to compare a path against something git
// itself reported (which always resolves symlinks), mirroring engine/
// local's resolvedOrSelf for the same reason. p need not exist (CommitPaths
// resolving a deletion or a rename's old path, say): when resolving it
// directly fails, this falls back to resolving its parent directory
// instead, which -- unlike p -- exists whenever git could see p change at
// all, and joins p's own base name back on.
func resolvedOrSelfPath(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	if realDir, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(realDir, filepath.Base(p))
	}
	return p
}

// checkIgnored reports whether git ignores rel inside root (`git
// check-ignore -q`): exit 0 means ignored, exit 1 means not ignored (not a
// failure), anything else is a real git error.
func (m *Manager) checkIgnored(ctx context.Context, root, rel string) (bool, error) {
	_, err := m.run(ctx, root, "check-ignore", "-q", "--", rel)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}
