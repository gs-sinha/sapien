package memory

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

// Locator resolves where a memory's canonical Markdown file lives, per its
// scope (PLAN.md §12).
type Locator struct {
	// WorkspaceDir is the workspace root (containing sapien.workspace.yaml).
	// Workspace-scoped memories live under WorkspaceDir/memories.
	WorkspaceDir string
	// ServiceDirs maps a service name to its resolved API package directory
	// (the "…/api" directory, PLAN §6). Service-scoped memories live under
	// ServiceDirs[name]/memories.
	ServiceDirs map[string]string
	// LocalDir is the workspace's local tier (<workspace>/local, PLAN §7b).
	// A flow-scoped memory whose flow is local-tier lives under
	// LocalDir/memories, so promoting the flow is what moves the memory
	// into the team's memories/ and nothing about a local flow leaks there
	// on its own. Empty means the workspace has no local tier: local-owned
	// flows then fall back to WorkspaceDir/memories.
	LocalDir string
	// FlowOwner reports which entity owns a flow ID: kind is "service" (with
	// id the owning service name), "local" (id ignored; see LocalDir), or
	// "workspace" (id ignored). A nil FlowOwner, or one returning an
	// unrecognized kind, is treated as "workspace" (PLAN §12: "flow ...
	// stored as workspace or service memory with subject.flow set").
	FlowOwner func(flowID string) (kind, id string)
	// ReadOnly names services whose package must not be written into; see
	// ReadOnlyServices. Shared by reference with the engine, like ServiceDirs.
	ReadOnly ReadOnlyServices
}

// DirFor returns the directory m's file should live in, given m.Scope (and,
// for service/flow scopes, m.Subject). It returns "" (with a nil error) for
// domain.ScopePersonal, meaning "no file; SQLite only".
//
// For domain.ScopeWorkspace it additionally honours m.Tier (PLAN §7b): the
// workspace tier (domain.TierWorkspace) is the team's WorkspaceDir/memories;
// the local tier (domain.TierLocal, and "" -- the default for a newly
// created workspace-scope memory, so a memory starts out on this machine
// only, same as a flow) is localMemoriesDir(). Callers that want to keep a
// memory in its current tier across an Update must carry that tier forward
// themselves (Store.Update does, from the existing row) -- DirFor has no
// memory of where a memory used to live.
func (l Locator) DirFor(m domain.Memory) (string, error) {
	var dir string
	var err error
	switch m.Scope {
	case domain.ScopePersonal:
		return "", nil
	case domain.ScopeWorkspace:
		dir = l.workspaceDirForTier(m.Tier)
	case domain.ScopeService:
		dir, err = l.serviceDirForSubject(m.Subject)
	case domain.ScopeFlow:
		dir, err = l.flowDir(m.Subject)
	default:
		return "", errs.New(errs.Invalid, "memory: unknown scope %q", m.Scope)
	}
	if err != nil {
		return "", err
	}
	return withFolder(dir, m.Folder)
}

// withFolder resolves dir/folderVal, normalizing and validating folderVal
// first (PLAN §34f item 6's folder rules, via internal/folder): "" (the
// default -- Create's caller left Folder unset, or a plain Update carrying
// it forward from the existing file) returns dir unchanged.
func withFolder(dir, folderVal string) (string, error) {
	norm, err := folder.NormalizeAndValidate(folderVal)
	if err != nil {
		return "", err
	}
	if norm == "" {
		return dir, nil
	}
	return filepath.Join(dir, filepath.FromSlash(norm)), nil
}

// workspaceDirForTier resolves the memories directory for scope=workspace,
// per the tier's name: TierWorkspace is the team's workspaceMemoriesDir();
// TierLocal, and "" (the default for a memory that never named a tier), is
// this machine's localMemoriesDir().
func (l Locator) workspaceDirForTier(tier string) string {
	if tier == domain.TierWorkspace {
		return l.workspaceMemoriesDir()
	}
	return l.localMemoriesDir()
}

func (l Locator) workspaceMemoriesDir() string {
	return filepath.Join(l.WorkspaceDir, domain.MemoriesDir)
}

// localMemoriesDir is where a local-tier flow's memories live, or the
// workspace memories dir when the Locator was built without a local tier.
func (l Locator) localMemoriesDir() string {
	if l.LocalDir == "" {
		return l.workspaceMemoriesDir()
	}
	return filepath.Join(l.LocalDir, domain.MemoriesDir)
}

// serviceDirForSubject resolves the memories directory for scope=service:
// subject.Service if set, else the service inferred from subject.Operation's
// "<service>.<op>" prefix.
func (l Locator) serviceDirForSubject(subj domain.Subject) (string, error) {
	svc := subj.Service
	if svc == "" {
		svc = serviceFromOperation(subj.Operation)
	}
	if svc == "" {
		return "", errs.New(errs.Invalid, "memory: service scope requires subject.service or subject.operation")
	}
	return l.serviceMemoriesDir(svc)
}

func (l Locator) serviceMemoriesDir(service string) (string, error) {
	dir, ok := l.ServiceDirs[service]
	if !ok {
		return "", errs.New(errs.ServiceNotFound, "memory: unknown service %q", service)
	}
	if l.ReadOnly[service] {
		return "", readOnlyErr(service)
	}
	return filepath.Join(dir, domain.MemoriesDir), nil
}

// flowDir resolves the memories directory for scope=flow: the flow's owner
// (via FlowOwner) -- the service's package, the local tier, or the
// workspace -- defaulting to the workspace when FlowOwner is nil or reports
// an unrecognized/workspace owner.
func (l Locator) flowDir(subj domain.Subject) (string, error) {
	if subj.Flow == "" {
		return "", errs.New(errs.Invalid, "memory: flow scope requires subject.flow")
	}
	if l.FlowOwner != nil {
		switch kind, id := l.FlowOwner(subj.Flow); kind {
		case domain.FlowOwnerService:
			return l.serviceMemoriesDir(id)
		case domain.FlowOwnerLocal:
			return l.localMemoriesDir(), nil
		}
	}
	return l.workspaceMemoriesDir(), nil
}

// serviceFromOperation extracts the service name from an operation ID of the
// form "<service-name>.<operationId>" (PLAN §5). Returns "" if opID has no
// service prefix.
func serviceFromOperation(opID string) string {
	if i := strings.Index(opID, "."); i > 0 {
		return opID[:i]
	}
	return ""
}

// Files returns every "*.md" file anywhere under every memory directory
// Locator knows about (the workspace's, its local tier's, and every known
// service's) -- at any depth, so a memory saved into a subfolder is indexed
// exactly like one at the root (PLAN §34f item 6) -- sorted
// lexicographically. A directory whose name starts with "." (the same rule
// the file watcher and reindexOwnerFlows apply) is skipped entirely, and a
// missing directory is skipped rather than treated as an error (a workspace
// or service with no memories yet has none, and most workspaces never grow
// a local tier).
func (l Locator) Files() ([]string, error) {
	var dirs []string
	if l.WorkspaceDir != "" {
		dirs = append(dirs, l.workspaceMemoriesDir())
	}
	if l.LocalDir != "" {
		dirs = append(dirs, filepath.Join(l.LocalDir, domain.MemoriesDir))
	}

	names := make([]string, 0, len(l.ServiceDirs))
	for name := range l.ServiceDirs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		dirs = append(dirs, filepath.Join(l.ServiceDirs[name], domain.MemoriesDir))
	}

	var files []string
	for _, dir := range dirs {
		found, err := walkMDFiles(dir)
		if err != nil {
			return nil, errs.Wrap(errs.Internal, err, "list memory files in %s", dir)
		}
		files = append(files, found...)
	}
	sort.Strings(files)
	return files, nil
}

// walkMDFiles returns every "*.md" file under dir, at any depth, skipping
// directories whose name starts with ".". A missing dir yields (nil, nil).
func walkMDFiles(dir string) ([]string, error) {
	var files []string
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
		if strings.HasSuffix(d.Name(), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return files, nil
}

// tierOfPath reports which tier the memory file at path lives in (PLAN
// §7b), from which known directory it falls under: LocalDir/memories ->
// TierLocal, WorkspaceDir/memories -> TierWorkspace, a known service's
// memories dir -> TierService. "" when path matches none of them (path is
// empty -- personal scope has no file -- or names a service no longer in
// ServiceDirs). There is no tier column in the memories table to read this
// back from, so every caller that hands a Memory to someone outside this
// package derives it fresh, from FilePath, every time: Store.Get/List after
// scanning a row, and writeFileForScope right after it resolves where a
// write lands.
//
// This applies uniformly regardless of Scope: a flow-scoped memory's file
// sits wherever its flow's tier put it (PLAN §7b's "Flow-scoped memories
// keep today's owner-following placement"), and tierOfPath reports that
// placement the same way it would for a workspace-scope memory -- Tier
// describes where the file is, not why.
func (l Locator) tierOfPath(path string) string {
	if path == "" {
		return ""
	}
	if l.LocalDir != "" && isUnderDir(path, filepath.Join(l.LocalDir, domain.MemoriesDir)) {
		return domain.TierLocal
	}
	if l.WorkspaceDir != "" && isUnderDir(path, l.workspaceMemoriesDir()) {
		return domain.TierWorkspace
	}
	for _, dir := range l.ServiceDirs {
		if isUnderDir(path, filepath.Join(dir, domain.MemoriesDir)) {
			return domain.TierService
		}
	}
	return ""
}

// folderOfPath reports the folder segment of the memory file at path,
// relative to whichever known memories directory it lives under -- the same
// directory match tierOfPath makes, just also keeping the remainder as a
// folder instead of collapsing it to a tier name. "" (the root) for a path
// directly inside a known directory, and for one matching none of them
// (including "" -- personal scope has no file).
func (l Locator) folderOfPath(path string) string {
	if path == "" {
		return ""
	}
	if l.LocalDir != "" {
		if dir := filepath.Join(l.LocalDir, domain.MemoriesDir); isUnderDir(path, dir) {
			return folder.FromAbs(dir, path)
		}
	}
	if l.WorkspaceDir != "" {
		if dir := l.workspaceMemoriesDir(); isUnderDir(path, dir) {
			return folder.FromAbs(dir, path)
		}
	}
	for _, dir := range l.ServiceDirs {
		if d := filepath.Join(dir, domain.MemoriesDir); isUnderDir(path, d) {
			return folder.FromAbs(d, path)
		}
	}
	return ""
}

// rootForPath returns the known memories directory (LocalDir/memories,
// WorkspaceDir/memories, or a known service's memories dir) that path lives
// under, or "" if none matches. Mirrors tierOfPath/folderOfPath's own
// directory matching; used to bound folder.CleanEmptyDirs so a cleanup
// after a move never walks above the kind's root for that tier.
func (l Locator) rootForPath(path string) string {
	if path == "" {
		return ""
	}
	if l.LocalDir != "" {
		if dir := filepath.Join(l.LocalDir, domain.MemoriesDir); isUnderDir(path, dir) {
			return dir
		}
	}
	if l.WorkspaceDir != "" {
		if dir := l.workspaceMemoriesDir(); isUnderDir(path, dir) {
			return dir
		}
	}
	for _, dir := range l.ServiceDirs {
		if d := filepath.Join(dir, domain.MemoriesDir); isUnderDir(path, d) {
			return d
		}
	}
	return ""
}

// isUnderDir reports whether path is dir itself or lives underneath it.
// Mirrors internal/example's Locator.isUnder.
func isUnderDir(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// ReadOnlyServices, when set on a Locator, names services whose package
// directory must not be written: a git-sourced service read from a managed
// clone that Sapien resets on every sync (PLAN §7b). Reads still cover
// their memories directory; DirFor refuses service and flow scopes that
// resolve into one, with a hint to bind a local checkout.
type ReadOnlyServices = map[string]bool

// readOnlyErr is the error DirFor returns for a service in ReadOnly.
func readOnlyErr(service string) error {
	return errs.New(errs.Invalid, "memory: service %q is read from a managed git clone that Sapien resets on every sync, so nothing can be written into it", service).
		WithDetail("service", service).
		WithHint("bind a local checkout with `sapien service bind " + service + " <path>` so contributions ride your own branch, or keep the memory at workspace scope with subject.service set")
}
