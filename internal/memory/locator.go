package memory

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
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
func (l Locator) DirFor(m domain.Memory) (string, error) {
	switch m.Scope {
	case domain.ScopePersonal:
		return "", nil
	case domain.ScopeWorkspace:
		return l.workspaceMemoriesDir(), nil
	case domain.ScopeService:
		return l.serviceDirForSubject(m.Subject)
	case domain.ScopeFlow:
		return l.flowDir(m.Subject)
	default:
		return "", errs.New(errs.Invalid, "memory: unknown scope %q", m.Scope)
	}
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

// Files returns every "*.md" file under every memory directory Locator
// knows about (the workspace's, its local tier's, and every known
// service's), sorted lexicographically. Missing directories are skipped
// rather than treated as errors (a workspace or service with no memories
// yet has none, and most workspaces never grow a local tier).
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
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, errs.Wrap(errs.Internal, err, "list memory files in %s", dir)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
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
