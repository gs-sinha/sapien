// Package folder is the one shared place that knows what a "folder" means
// for a flow, memory, or example (PLAN §34f item 6): a flow, memory, or
// example may live in a subfolder of its kind's directory at any tier. The
// folder is read from the file's path -- never stored as a column or a
// front-matter/YAML field -- so it is orthogonal to tier and ids stay
// global, exactly as PLAN.md describes: "moving never breaks a reference."
//
// Every package that carries a Folder field (internal/memory,
// internal/example, internal/engine/local's flow handling) normalizes and
// validates it through this package, so "a/b", "a/b/", "/a/b", and "" all
// mean the same thing everywhere a folder string is accepted.
package folder

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gs-sinha/sapien/internal/errs"
)

// MaxDepth is the maximum number of segments a folder may have.
const MaxDepth = 8

// MaxLength is the maximum length, in bytes, of a normalized folder string.
const MaxLength = 200

// Normalize trims whitespace and leading/trailing slashes so "", "/", "  /
// ", "a/b", "a/b/", and "/a/b" all collapse to the same canonical form ("a/b",
// or "" for the root). It does not validate; call Validate (or
// NormalizeAndValidate) on the result before using it to build a path.
func Normalize(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "/")
	return s
}

// Validate checks a normalized folder string against PLAN §34f item 6's
// rules: each segment non-empty, no "." or ".." segment, no backslash, no
// leading-dot (hidden) segment, no absolute path, at most MaxDepth segments,
// at most MaxLength characters. "" (the root) is always valid. Every
// rejection is an errs.Invalid error with a hint.
func Validate(normalized string) error {
	if normalized == "" {
		return nil
	}
	if len(normalized) > MaxLength {
		return errs.New(errs.Invalid, "folder %q is too long (max %d characters)", normalized, MaxLength).
			WithHint("use a shorter folder path")
	}
	if strings.Contains(normalized, "\\") {
		return errs.New(errs.Invalid, "folder %q must not contain a backslash", normalized).
			WithHint(`use "/" to separate segments, not "\\"`)
	}
	if strings.HasPrefix(normalized, "/") {
		return errs.New(errs.Invalid, "folder %q must not be an absolute path", normalized).
			WithHint("pass a path relative to the kind's directory, e.g. \"a/b\"")
	}
	segs := strings.Split(normalized, "/")
	if len(segs) > MaxDepth {
		return errs.New(errs.Invalid, "folder %q is nested too deep (max %d levels)", normalized, MaxDepth).
			WithHint("use a shallower folder")
	}
	for _, seg := range segs {
		if seg == "" {
			return errs.New(errs.Invalid, "folder %q has an empty segment (e.g. a doubled \"/\")", normalized).
				WithHint("remove the empty segment")
		}
		if seg == "." || seg == ".." {
			return errs.New(errs.Invalid, "folder %q must not contain a %q segment", normalized, seg).
				WithHint("use a literal folder name, not \".\" or \"..\"")
		}
		if strings.HasPrefix(seg, ".") {
			return errs.New(errs.Invalid, "folder %q segment %q must not start with \".\"", normalized, seg).
				WithHint("hidden directories are reserved and are never indexed")
		}
	}
	return nil
}

// NormalizeAndValidate normalizes raw and validates the result, returning
// the canonical folder string or the first validation problem.
func NormalizeAndValidate(raw string) (string, error) {
	n := Normalize(raw)
	if err := Validate(n); err != nil {
		return "", err
	}
	return n, nil
}

// Of derives a folder from a "/"-separated relative path (already relative
// to the kind's root directory, forward slashes or not): the directory
// portion, minus the file name itself, or "" when the path has no
// directory component. Mirrors how a flow/memory/example's own file name
// sits directly under its folder.
func Of(relPath string) string {
	rel := filepath.ToSlash(relPath)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return ""
	}
	dir := path.Dir(rel)
	if dir == "." || dir == "/" {
		return ""
	}
	return strings.Trim(dir, "/")
}

// FromAbs derives a folder from abs, an absolute file path known to live
// under root (the kind's root directory for one tier/owner, e.g.
// "<workspace>/flows" or "<service>/api/memories"). Returns "" when abs is
// not under root, is root itself, or sits directly inside root.
func FromAbs(root, abs string) string {
	if root == "" || abs == "" {
		return ""
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	return Of(rel)
}

// Join returns the "/"-separated relative path for a file named name inside
// folderVal ("" -> name; "a/b" -> "a/b/name").
func Join(folderVal, name string) string {
	if folderVal == "" {
		return name
	}
	return folderVal + "/" + name
}

// HasPrefix reports whether folderVal is prefix itself or nested under it:
// list-filter prefix semantics (PLAN §34f item 4) where prefix "" matches
// everything, "a" matches "a" and "a/b", but not "ab". Both arguments are
// expected to already be normalized (Normalize).
func HasPrefix(folderVal, prefix string) bool {
	if prefix == "" {
		return true
	}
	if folderVal == prefix {
		return true
	}
	return strings.HasPrefix(folderVal, prefix+"/")
}

// CleanEmptyDirs removes leafDir, and then each of its ancestors in turn,
// as long as each is empty and still lies inside rootDir -- but never
// rootDir itself. Used after a move empties out a folder a flow, memory, or
// example used to sit in. Best effort: any error (permissions, a directory
// that turns out not to be empty because of a concurrent write) simply
// stops the walk early rather than failing the move that already
// succeeded.
func CleanEmptyDirs(leafDir, rootDir string) {
	leafDir = filepath.Clean(leafDir)
	rootDir = filepath.Clean(rootDir)
	if leafDir == rootDir {
		return
	}
	// leafDir must be inside rootDir for this to ever be safe to remove.
	rel, err := filepath.Rel(rootDir, leafDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	for leafDir != rootDir {
		entries, err := os.ReadDir(leafDir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(leafDir); err != nil {
			return
		}
		leafDir = filepath.Dir(leafDir)
	}
}

// MoveFile renames src to dst, falling back to copy-then-remove when the
// two are on different filesystems (the one case rename cannot serve).
// Shared by internal/memory and internal/example's folder moves; mirrors
// internal/engine/local's own unexported moveFile for flows.
func MoveFile(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return errs.Wrap(errs.Internal, err, "moving %s to %s", src, dst)
	}
	in, err := os.Open(src)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "reading %s", src)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "creating %s", dst)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return errs.Wrap(errs.Internal, err, "copying %s to %s", src, dst)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return errs.Wrap(errs.Internal, err, "closing %s", dst)
	}
	if err := os.Remove(src); err != nil {
		return errs.Wrap(errs.Internal, err, "removing %s after copying it to %s", src, dst)
	}
	return nil
}
