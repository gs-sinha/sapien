package example

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

// ExamplesDir is the subdirectory name examples live under, workspace or
// service scoped (PLAN.md §34b): "<workspace>/examples/" or
// "<service package dir>/examples/".
const ExamplesDir = "examples"

// exampleFileSuffix is the suffix every example file carries: "<id>.example.yaml".
const exampleFileSuffix = ".example.yaml"

// FileName returns the file name (no directory) an example with the given ID
// is stored under.
func FileName(id string) string {
	return id + exampleFileSuffix
}

// Locator resolves where a saved example's file lives, per its scope
// (PLAN.md §34b). It mirrors memory.Locator's role for the memory domain.
type Locator struct {
	// WorkspaceDir is the workspace root (containing sapien.workspace.yaml).
	// Workspace-scoped examples live under WorkspaceDir/examples.
	WorkspaceDir string
	// ServiceDirs maps a service name to its resolved API package directory
	// (the "…/api" directory, PLAN §6). Service-scoped examples live under
	// ServiceDirs[name]/examples.
	ServiceDirs map[string]string
}

// PathFor returns the file path ex's file should live at, given ex.Scope
// (and, for service scope, ex.Service, which callers derive from
// ex.Operation before calling PathFor). An empty Scope is treated as
// workspace scope.
func (l Locator) PathFor(ex domain.SavedExample) (string, error) {
	switch ex.Scope {
	case domain.ExampleScopeWorkspace, "":
		return filepath.Join(l.workspaceExamplesDir(), FileName(ex.ID)), nil
	case domain.ExampleScopeService:
		dir, ok := l.ServiceDirs[ex.Service]
		if !ok {
			return "", errs.New(errs.Invalid, "example: unknown service %q for service-scoped example", ex.Service).WithDetail("service", ex.Service)
		}
		return filepath.Join(dir, ExamplesDir, FileName(ex.ID)), nil
	default:
		return "", errs.New(errs.Invalid, "example: unknown scope %q", ex.Scope)
	}
}

func (l Locator) workspaceExamplesDir() string {
	return filepath.Join(l.WorkspaceDir, ExamplesDir)
}

func (l Locator) serviceExamplesDir(service string) (string, bool) {
	dir, ok := l.ServiceDirs[service]
	if !ok {
		return "", false
	}
	return filepath.Join(dir, ExamplesDir), true
}

// scopedFile is one example file discovered by walking the workspace and
// every known service's examples directory, tagged with the scope its
// directory implies.
type scopedFile struct {
	path  string
	scope domain.ExampleScope
}

// files returns every "*.example.yaml" file under the workspace's examples
// directory and every known service's examples directory, tagged with scope,
// sorted lexicographically by path. Missing directories are skipped rather
// than treated as errors (a workspace or service with no examples yet has
// none), mirroring memory.Locator.Files.
func (l Locator) files() ([]scopedFile, error) {
	var out []scopedFile

	add := func(dir string, scope domain.ExampleScope) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return errs.Wrap(errs.Internal, err, "list example files in %s", dir)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), exampleFileSuffix) {
				continue
			}
			out = append(out, scopedFile{path: filepath.Join(dir, e.Name()), scope: scope})
		}
		return nil
	}

	if l.WorkspaceDir != "" {
		if err := add(l.workspaceExamplesDir(), domain.ExampleScopeWorkspace); err != nil {
			return nil, err
		}
	}

	names := make([]string, 0, len(l.ServiceDirs))
	for name := range l.ServiceDirs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		dir, _ := l.serviceExamplesDir(name)
		if err := add(dir, domain.ExampleScopeService); err != nil {
			return nil, err
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// scopeForPath reports which scope the example file at path belongs to,
// based on which known directory (workspace or a service's) it lives under.
// It defaults to workspace scope when path matches no known directory (e.g.
// a service directory that was removed from ServiceDirs after the file was
// written). Used by IndexOne, which is handed a bare path by a file watcher
// and has no other way to know the example's scope.
func (l Locator) scopeForPath(path string) domain.ExampleScope {
	if isUnder(path, l.workspaceExamplesDir()) {
		return domain.ExampleScopeWorkspace
	}
	for name := range l.ServiceDirs {
		dir, _ := l.serviceExamplesDir(name)
		if isUnder(path, dir) {
			return domain.ExampleScopeService
		}
	}
	return domain.ExampleScopeWorkspace
}

// isUnder reports whether path is dir itself or lives underneath it.
func isUnder(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
