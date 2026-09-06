package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"

	"github.com/growsimplee/sapien/internal/domain"
)

// fingerprintEntry is one (relative path, content hash) pair going into a
// PackageFingerprint.
type fingerprintEntry struct {
	rel  string
	hash string
}

// PackageFingerprint computes a cheap, order-independent digest over every
// file that could affect pkg's snapshot: its contracts, service.yaml, every
// docs/**/*.md file, and every flows/*.flow.yaml file. It changes whenever
// any of those files' contents change, and is meant for a cheap "is this
// service stale" check (e.g. the CLI), not as a replacement for actually
// rebuilding the snapshot.
//
// extra folds additional, caller-supplied values into the digest, in the
// order given. Its intended use is a git source's resolved commit (PLAN
// §18): two commits can produce byte-identical file contents (an empty
// commit, a no-op merge), so a caller that needs the fingerprint to also
// change when the commit does should pass it, e.g.
// PackageFingerprint(pkg, checkout.Commit). Existing callers that only care
// about file contents pass no extra arguments and see no change in
// behavior.
func PackageFingerprint(pkg *Package, extra ...string) (string, error) {
	var entries []fingerprintEntry

	add := func(abs string) error {
		h, err := hashFileHex(abs)
		if err != nil {
			return err
		}
		entries = append(entries, fingerprintEntry{rel: relPath(pkg.Dir, abs), hash: h})
		return nil
	}

	for _, c := range pkg.Contracts {
		if err := add(c); err != nil {
			return "", err
		}
	}
	if pkg.MetadataFile != "" {
		if err := add(pkg.MetadataFile); err != nil {
			return "", err
		}
	}
	if pkg.DocsDir != "" {
		files, err := globFilesRecursive(pkg.DocsDir, ".md")
		if err != nil {
			return "", err
		}
		for _, f := range files {
			if err := add(f); err != nil {
				return "", err
			}
		}
	}
	if pkg.FlowsDir != "" {
		matches, err := filepath.Glob(filepath.Join(pkg.FlowsDir, "*"+domain.FlowFileSuffix))
		if err != nil {
			return "", err
		}
		for _, f := range matches {
			if err := add(f); err != nil {
				return "", err
			}
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })

	h := sha256.New()
	for _, e := range entries {
		h.Write([]byte(e.rel))
		h.Write([]byte{0})
		h.Write([]byte(e.hash))
		h.Write([]byte{0})
	}
	for _, e := range extra {
		h.Write([]byte("extra"))
		h.Write([]byte{0})
		h.Write([]byte(e))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
