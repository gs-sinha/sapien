// Package selfupdate resolves how this sapien binary was installed and
// checks GitHub Releases for a newer version (PLAN.md §34f item 4).
//
// Both internal/cli (the `sapien upgrade` command, and the daemon's
// background check kicked from serve.go) and internal/server (GET
// /v1/daemon's install_method, and the GET/POST /v1/update handlers) use
// this package; it depends on neither of them (only on internal/config, for
// where "~/.sapien" is, and internal/errs), which is exactly what lets
// internal/server call it directly even though internal/server cannot
// import internal/cli.
package selfupdate

import (
	"go/build"
	"os"
	"path/filepath"
	"strings"

	"github.com/gs-sinha/sapien/internal/errs"
)

// modulePath is this repository's module path (go.mod), used to recognize
// a binary built from a checkout of it rather than installed elsewhere.
const modulePath = "github.com/gs-sinha/sapien"

// Method is how the running sapien binary got onto this machine.
type Method string

const (
	MethodHomebrew Method = "homebrew"
	MethodGo       Method = "go"
	MethodDev      Method = "dev"
	MethodScript   Method = "script"
)

// Executable returns the running sapien binary's path, resolved through any
// symlink (os.Executable alone does not: on Homebrew the binary on PATH is
// a symlink into the Cellar, and DetectMethod needs the real Cellar path to
// recognize it). If the symlink cannot be resolved, the unresolved path is
// returned instead of an error -- install_method then degrades to a guess
// rather than the whole request failing.
func Executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", errs.Wrap(errs.Internal, err, "resolving the running sapien binary's path")
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved, nil
	}
	return p, nil
}

// DetectMethod classifies how the sapien binary at resolvedPath (as
// Executable returns it) was installed, given the running version. Order
// matters: a Homebrew Cellar path is checked before GOBIN/GOPATH (both are
// just directories nothing stops from coinciding), and "dev" -- version
// "dev" (the ldflags default when Makefile's `git describe` finds no tags)
// or a checkout of this module -- is checked last among the specific cases,
// before falling back to "script" (an install.sh install, or anything else
// not otherwise recognized).
func DetectMethod(version, resolvedPath string) Method {
	if isHomebrewPath(resolvedPath) {
		return MethodHomebrew
	}
	if isGoBinPath(resolvedPath) {
		return MethodGo
	}
	if version == "dev" || inModuleCheckout(resolvedPath) {
		return MethodDev
	}
	return MethodScript
}

// isHomebrewPath reports whether p is inside a Homebrew Cellar: covers
// Intel (/usr/local/Cellar/...), Apple Silicon (/opt/homebrew/Cellar/...),
// and Linuxbrew (/home/linuxbrew/.linuxbrew/Cellar/...) without hardcoding
// any of those roots -- "/Cellar/" and "/homebrew/" are the two substrings
// every one of them has at least one of.
func isHomebrewPath(p string) bool {
	return strings.Contains(p, "/Cellar/") || strings.Contains(p, "/homebrew/")
}

// isGoBinPath reports whether p's directory is $GOBIN, or "bin" under any
// entry of $GOPATH (or its default when unset) -- where `go install` places
// a built binary.
func isGoBinPath(p string) bool {
	dir := filepath.Clean(filepath.Dir(p))

	if gobin := os.Getenv("GOBIN"); gobin != "" && filepath.Clean(gobin) == dir {
		return true
	}

	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		gopath = build.Default.GOPATH
	}
	for _, root := range filepath.SplitList(gopath) {
		if root == "" {
			continue
		}
		if filepath.Clean(filepath.Join(root, "bin")) == dir {
			return true
		}
	}
	return false
}

// inModuleCheckout reports whether p sits inside a directory tree whose
// nearest go.mod declares modulePath -- i.e. this is a `make build` (or
// `go build`) output sitting in a checkout of this repository, as opposed
// to a binary that merely happens to live under some other Go module's
// directory (a go.mod for a *different* module stops the walk rather than
// being climbed past).
func inModuleCheckout(p string) bool {
	dir := filepath.Dir(p)
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			return declaresModule(data, modulePath)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// declaresModule reports whether gomod's `module` directive names module.
func declaresModule(gomod []byte, module string) bool {
	for _, line := range strings.Split(string(gomod), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module"); ok {
			return strings.TrimSpace(rest) == module
		}
	}
	return false
}

// Command returns the exact shell command a user with this install method
// should run to upgrade.
func Command(m Method) string {
	switch m {
	case MethodHomebrew:
		return "brew upgrade gs-sinha/tap/sapien"
	case MethodGo:
		return "go install " + modulePath + "/cmd/sapien@latest"
	case MethodDev:
		return "git pull && make build"
	default:
		return "sapien upgrade"
	}
}

// CanSelfUpgrade reports whether `sapien upgrade` (and POST
// /v1/update/apply) can replace the binary in place: only true for a
// script install, where the binary is a plain file this process itself can
// overwrite -- a Homebrew, go install, or dev build should go through that
// tool instead, or it drifts out of sync with what manages it.
func CanSelfUpgrade(m Method) bool { return m == MethodScript }
