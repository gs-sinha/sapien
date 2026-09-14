package workspace

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// localFile is the on-disk shape of sapien.workspace.local.yaml (PLAN §7b):
// the per-machine, gitignored companion of the committed workspace file.
// It is keyed by service name rather than being a list because an entry
// can only ever say one thing about one service -- "read it from here" --
// and a map cannot express two conflicting overrides for the same name.
type localFile struct {
	Version  int                      `yaml:"version"`
	Services map[string]localOverride `yaml:"services,omitempty"`
}

// localOverride is one entry: the local checkout this machine reads the
// service from, and optionally the contract file inside it.
type localOverride struct {
	Path     string `yaml:"path"`
	Contract string `yaml:"contract,omitempty"`
}

// LocalOverridePath returns the absolute path of ws's
// sapien.workspace.local.yaml, whether or not it exists.
func LocalOverridePath(ws *domain.Workspace) string {
	return filepath.Join(ws.Dir, domain.WorkspaceLocalFileName)
}

// LoadLocal applies ws's sapien.workspace.local.yaml, if there is one, to
// ws in memory: for every entry naming a registered service, the committed
// Source moves to ServiceRef.Team and Source becomes a local source at the
// entry's path. Load calls it, so every reader of a workspace -- the
// one-shot CLI, the daemon, the multi-workspace manager -- sees the source
// this machine actually reads without having to know the override file
// exists; Save writes Team back (committedView), which is what keeps the
// override out of the committed file.
//
// An absent file is not an error: most machines have none. An entry naming
// a service the workspace does not register is ignored rather than
// rejected, because the committed file can drop a service after a
// developer bound it and their machine must keep working; SaveLocal writes
// only overrides that still apply, so such an entry disappears on the next
// bind or unbind.
func LoadLocal(ws *domain.Workspace) error {
	path := LocalOverridePath(ws)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errs.Wrap(errs.Internal, err, "reading %s", path)
	}

	var lf localFile
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return errs.Wrap(errs.Invalid, err, "parsing %s", path).WithDetail("file", path)
	}
	if lf.Version != 1 {
		return errs.New(errs.Invalid, "unsupported version %d in %s (want 1)", lf.Version, path).
			WithDetail("file", path)
	}

	for i := range ws.Services {
		ref := &ws.Services[i]
		entry, ok := lf.Services[ref.Name]
		if !ok {
			continue
		}
		if entry.Path == "" {
			return errs.New(errs.Invalid, "service %q in %s has no path", ref.Name, path).
				WithDetail("file", path).WithDetail("name", ref.Name)
		}
		applyOverride(ref, entry.Path, entry.Contract)
	}
	return nil
}

// applyOverride makes ref read from the local checkout at path, keeping the
// committed source in Team. Applying a second override to an already
// overridden ref replaces the path but keeps the original Team: the
// committed source is the one thing an override must never overwrite,
// because it is what Save writes back.
func applyOverride(ref *domain.ServiceRef, path, contract string) {
	committed := ref.Source
	if ref.Team != nil {
		committed = *ref.Team
	}
	if contract == "" {
		contract = committed.Contract
	}
	team := committed
	ref.Team = &team
	ref.Source = domain.Source{Kind: domain.SourceLocal, Path: path, Contract: contract}
}

// SaveLocal writes sapien.workspace.local.yaml from every service that
// carries an override (Team != nil), atomically like Save, and removes the
// file when no override remains so a machine that has unbound everything
// is indistinguishable from one that never bound anything. Paths are
// written as given ("~" included); ResolveSourcePath expands them on read.
func SaveLocal(ws *domain.Workspace) error {
	if ws.Dir == "" {
		return errs.New(errs.Invalid, "workspace has no Dir set")
	}
	path := LocalOverridePath(ws)

	lf := localFile{Version: 1, Services: map[string]localOverride{}}
	for _, ref := range ws.Services {
		if ref.Team == nil {
			continue
		}
		entry := localOverride{Path: ref.Source.Path}
		// The contract is only an override when it differs from the
		// committed one; writing it otherwise would pin the local file to a
		// value the committed file may later change.
		if ref.Source.Contract != ref.Team.Contract {
			entry.Contract = ref.Source.Contract
		}
		lf.Services[ref.Name] = entry
	}

	if len(lf.Services) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return errs.Wrap(errs.Internal, err, "removing %s", path)
		}
		return nil
	}

	data, err := encodeYAML(lf)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "encoding %s", domain.WorkspaceLocalFileName)
	}
	return atomicWrite(path, data)
}

// Bind makes ws read the named service from the local checkout at path,
// in memory only; SaveLocal persists it. Binding an already bound service
// moves it to the new path and keeps the original committed source.
func Bind(ws *domain.Workspace, name, path string) error {
	if path == "" {
		return errs.New(errs.Invalid, "bind: path must not be empty").WithDetail("name", name)
	}
	for i := range ws.Services {
		if ws.Services[i].Name == name {
			applyOverride(&ws.Services[i], path, "")
			return nil
		}
	}
	return errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
}

// Unbind restores the named service's committed source, in memory only;
// SaveLocal persists it. A service without an override is left alone, so
// an unbind is safe to repeat.
func Unbind(ws *domain.Workspace, name string) error {
	for i := range ws.Services {
		ref := &ws.Services[i]
		if ref.Name != name {
			continue
		}
		if ref.Team != nil {
			ref.Source = *ref.Team
			ref.Team = nil
		}
		return nil
	}
	return errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
}

// EnsureLocalIgnored makes sure ws's root .gitignore lists
// sapien.workspace.local.yaml, creating the file or appending the line.
// The override file names paths on one developer's disk, so committing it
// would rebind every teammate's machine to directories they do not have;
// Init calls this for new workspaces and Bind for ones created before the
// file existed. Idempotent: an existing entry, with or without a leading
// slash, is left as it is.
func EnsureLocalIgnored(ws *domain.Workspace) error {
	path := filepath.Join(ws.Dir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return errs.Wrap(errs.Internal, err, "reading %s", path)
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == domain.WorkspaceLocalFileName || line == "/"+domain.WorkspaceLocalFileName {
			return nil
		}
	}

	var b strings.Builder
	b.Write(data)
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(domain.WorkspaceLocalFileName + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return errs.Wrap(errs.Internal, err, "writing %s", path)
	}
	return nil
}
