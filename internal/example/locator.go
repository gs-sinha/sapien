package example

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/folder"
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
	// Workspace-scoped examples live under WorkspaceDir/examples (the
	// workspace tier) or LocalDir/examples (the local tier); see PathFor.
	WorkspaceDir string
	// LocalDir is the workspace's local tier (<workspace>/local, PLAN §7b),
	// mirroring memory.Locator.LocalDir. Empty means the workspace has no
	// local tier: a workspace-scope example then always lands in
	// WorkspaceDir/examples regardless of its Tier.
	LocalDir string
	// ServiceDirs maps a service name to its resolved API package directory
	// (the "…/api" directory, PLAN §6). Service-scoped examples live under
	// ServiceDirs[name]/examples.
	ServiceDirs map[string]string
	// ReadOnly names services whose package must not be written into (a
	// git source read from a managed clone). Shared by reference with the
	// engine, like ServiceDirs; reads still cover their examples directory.
	ReadOnly map[string]bool
}

// PathFor returns the file path ex's file should live at, given ex.Scope
// (and, for service scope, ex.Service, which callers derive from
// ex.Operation before calling PathFor). An empty Scope is treated as
// workspace scope.
//
// For workspace scope it additionally honours ex.Tier (PLAN §7b), mirroring
// memory.Locator.DirFor: the workspace tier (domain.TierWorkspace) is the
// team's workspaceExamplesDir(); the local tier (domain.TierLocal, and ""
// -- the default for a newly created workspace-scope example) is
// localExamplesDir(). A caller that wants to keep an example in its current
// tier across an Update must carry that tier forward itself (Store.Update
// does, from the existing row).
func (l Locator) PathFor(ex domain.SavedExample) (string, error) {
	switch ex.Scope {
	case domain.ExampleScopeWorkspace, "":
		return withFolder(l.workspaceDirForTier(ex.Tier), ex.Folder, FileName(ex.ID))
	case domain.ExampleScopeService:
		dir, ok := l.ServiceDirs[ex.Service]
		if !ok {
			return "", errs.New(errs.Invalid, "example: unknown service %q for service-scoped example", ex.Service).WithDetail("service", ex.Service)
		}
		if l.ReadOnly[ex.Service] {
			return "", readOnlyErr(ex.Service)
		}
		return withFolder(filepath.Join(dir, ExamplesDir), ex.Folder, FileName(ex.ID))
	default:
		return "", errs.New(errs.Invalid, "example: unknown scope %q", ex.Scope)
	}
}

// withFolder resolves dir/folderVal/name, normalizing and validating
// folderVal first (PLAN §34f item 6, via internal/folder): "" (the default
// -- Create's caller left Folder unset, or a plain Update carrying it
// forward from the existing file) puts name directly in dir. Mirrors
// internal/memory.Locator's own withFolder.
func withFolder(dir, folderVal, name string) (string, error) {
	norm, err := folder.NormalizeAndValidate(folderVal)
	if err != nil {
		return "", err
	}
	if norm == "" {
		return filepath.Join(dir, name), nil
	}
	return filepath.Join(dir, filepath.FromSlash(norm), name), nil
}

func (l Locator) workspaceExamplesDir() string {
	return filepath.Join(l.WorkspaceDir, ExamplesDir)
}

// workspaceDirForTier resolves the examples directory for scope=workspace,
// per the tier's name: TierWorkspace is the team's workspaceExamplesDir();
// TierLocal, and "" (the default for an example that never named a tier),
// is this machine's localExamplesDir().
func (l Locator) workspaceDirForTier(tier string) string {
	if tier == domain.TierWorkspace {
		return l.workspaceExamplesDir()
	}
	return l.localExamplesDir()
}

// localExamplesDir is where a local-tier example lives, or
// workspaceExamplesDir() when the Locator was built without a local tier
// (LocalDir == ""), mirroring memory.Locator.localMemoriesDir.
func (l Locator) localExamplesDir() string {
	if l.LocalDir == "" {
		return l.workspaceExamplesDir()
	}
	return filepath.Join(l.LocalDir, ExamplesDir)
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

// files returns every "*.example.yaml" file anywhere under the workspace's
// examples directory and every known service's examples directory -- at any
// depth, so an example saved into a subfolder is indexed exactly like one at
// the root (PLAN §34f item 6) -- tagged with scope, sorted lexicographically
// by path. A directory whose name starts with "." is skipped entirely, and a
// missing directory is skipped rather than treated as an error (a workspace
// or service with no examples yet has none), mirroring memory.Locator.Files.
func (l Locator) files() ([]scopedFile, error) {
	var out []scopedFile

	add := func(dir string, scope domain.ExampleScope) error {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				if path != dir && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(d.Name(), exampleFileSuffix) {
				out = append(out, scopedFile{path: path, scope: scope})
			}
			return nil
		})
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return errs.Wrap(errs.Internal, err, "list example files in %s", dir)
		}
		return nil
	}

	if l.WorkspaceDir != "" {
		if err := add(l.workspaceExamplesDir(), domain.ExampleScopeWorkspace); err != nil {
			return nil, err
		}
	}
	if l.LocalDir != "" {
		// The local tier is still workspace *scope* -- Tier, derived
		// separately (tierOfPath), is what distinguishes it; readFile's
		// scope parameter only ever needs "workspace" or "service".
		if err := add(filepath.Join(l.LocalDir, ExamplesDir), domain.ExampleScopeWorkspace); err != nil {
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

// tierOfPath reports which tier the example file at path lives in (PLAN
// §7b), from which known directory it falls under: LocalDir/examples ->
// TierLocal, WorkspaceDir/examples -> TierWorkspace, a known service's
// examples dir -> TierService. "" when path matches none of them (path is
// empty, or names a service no longer in ServiceDirs). There is no tier
// column in the examples table to read this back from, so it is derived
// fresh from Path every time an example is handed to a caller: Store.Get,
// hydrate (List/ForOperations), and Create/Update right after they resolve
// where a write lands, mirroring memory.Locator.tierOfPath.
func (l Locator) tierOfPath(path string) string {
	if path == "" {
		return ""
	}
	if l.LocalDir != "" && isUnder(path, filepath.Join(l.LocalDir, ExamplesDir)) {
		return domain.TierLocal
	}
	if l.WorkspaceDir != "" && isUnder(path, l.workspaceExamplesDir()) {
		return domain.TierWorkspace
	}
	for name := range l.ServiceDirs {
		if dir, ok := l.serviceExamplesDir(name); ok && isUnder(path, dir) {
			return domain.TierService
		}
	}
	return ""
}

// folderOfPath reports the folder segment of the example file at path,
// relative to whichever known examples directory it lives under -- the same
// directory match tierOfPath makes, just also keeping the remainder as a
// folder instead of collapsing it to a tier name. "" for a path directly
// inside a known directory, and for one matching none of them.
func (l Locator) folderOfPath(path string) string {
	if path == "" {
		return ""
	}
	if l.LocalDir != "" {
		if dir := filepath.Join(l.LocalDir, ExamplesDir); isUnder(path, dir) {
			return folder.FromAbs(dir, path)
		}
	}
	if l.WorkspaceDir != "" {
		if dir := l.workspaceExamplesDir(); isUnder(path, dir) {
			return folder.FromAbs(dir, path)
		}
	}
	for name := range l.ServiceDirs {
		if dir, ok := l.serviceExamplesDir(name); ok && isUnder(path, dir) {
			return folder.FromAbs(dir, path)
		}
	}
	return ""
}

// rootForPath returns the known examples directory (LocalDir/examples,
// WorkspaceDir/examples, or a known service's examples dir) that path lives
// under, or "" if none matches. Mirrors tierOfPath/folderOfPath's own
// directory matching; used to bound folder.CleanEmptyDirs so a cleanup
// after a move never walks above the kind's root for that tier.
func (l Locator) rootForPath(path string) string {
	if path == "" {
		return ""
	}
	if l.LocalDir != "" {
		if dir := filepath.Join(l.LocalDir, ExamplesDir); isUnder(path, dir) {
			return dir
		}
	}
	if l.WorkspaceDir != "" {
		if dir := l.workspaceExamplesDir(); isUnder(path, dir) {
			return dir
		}
	}
	for name := range l.ServiceDirs {
		if dir, ok := l.serviceExamplesDir(name); ok && isUnder(path, dir) {
			return dir
		}
	}
	return ""
}

// isUnder reports whether path is dir itself or lives underneath it.
func isUnder(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// readOnlyErr is the error PathFor returns for a service the Locator's
// ReadOnly set names: one read from a managed git clone that Sapien resets
// on every sync (PLAN §7b).
func readOnlyErr(service string) error {
	return errs.New(errs.Invalid, "example: service %q is read from a managed git clone that Sapien resets on every sync, so nothing can be written into it", service).
		WithDetail("service", service).
		WithHint("bind a local checkout with `sapien service bind " + service + " <path>` so contributions ride your own branch, or save the example at workspace scope")
}
