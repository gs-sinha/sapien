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
// service from, its ref override, or both (PLAN §34f item 2). At least one
// of Path or Ref is set; Contract only ever applies alongside Path.
type localOverride struct {
	Path     string `yaml:"path,omitempty"`
	Ref      string `yaml:"ref,omitempty"`
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
		if entry.Path == "" && entry.Ref == "" {
			return errs.New(errs.Invalid, "service %q in %s has neither path nor ref", ref.Name, path).
				WithDetail("file", path).WithDetail("name", ref.Name)
		}
		applyOverride(ref, entry.Path, entry.Ref, entry.Contract)
	}
	return nil
}

// committedSourceOf returns the source every other machine reads ref from:
// ref.Team when this machine already overrides it, else ref.Source itself.
func committedSourceOf(ref domain.ServiceRef) domain.Source {
	if ref.Team != nil {
		return *ref.Team
	}
	return ref.Source
}

// serviceRefPtr returns a pointer to ws's service named name, so Bind,
// Unbind, SetLocalRef and ClearLocalRef can all mutate it in place.
func serviceRefPtr(ws *domain.Workspace, name string) (*domain.ServiceRef, bool) {
	for i := range ws.Services {
		if ws.Services[i].Name == name {
			return &ws.Services[i], true
		}
	}
	return nil, false
}

// applyOverride makes ref read from path (a local checkout), refOverride (a
// ref override with no path), or both -- path wins for reading when both
// are given, per sapien.workspace.local.yaml's contract (PLAN §34f item 2):
// an entry may carry only path, only ref, or both, and refOverride is kept
// even when path makes it moot, so it becomes effective again if the path
// override is later removed (Unbind). Applying a second override to an
// already overridden ref keeps the original Team: the committed source is
// the one thing an override must never overwrite, because it is what Save
// writes back.
func applyOverride(ref *domain.ServiceRef, path, refOverride, contract string) {
	committed := committedSourceOf(*ref)
	team := committed
	ref.Team = &team
	ref.LocalRef = refOverride

	if path != "" {
		if contract == "" {
			contract = committed.Contract
		}
		ref.Source = domain.Source{Kind: domain.SourceLocal, Path: path, Contract: contract}
		return
	}

	src := committed
	if refOverride != "" {
		src.Ref = refOverride
	}
	ref.Source = src
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
		hasPath := ref.Team != nil && ref.Source.Kind == domain.SourceLocal
		hasRef := ref.LocalRef != ""
		if !hasPath && !hasRef {
			continue
		}
		entry := localOverride{Ref: ref.LocalRef}
		if hasPath {
			entry.Path = ref.Source.Path
			// The contract is only an override when it differs from the
			// committed one; writing it otherwise would pin the local file
			// to a value the committed file may later change.
			if ref.Source.Contract != ref.Team.Contract {
				entry.Contract = ref.Source.Contract
			}
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

// Bind makes ws read the named service from the local checkout at path, in
// memory only; SaveLocal persists it. Binding an already bound service
// moves it to the new path and keeps the original committed source. Any
// existing ref override (LocalRef) is kept, not cleared -- it becomes
// effective again if the path override is later removed (Unbind).
func Bind(ws *domain.Workspace, name, path string) error {
	if path == "" {
		return errs.New(errs.Invalid, "bind: path must not be empty").WithDetail("name", name)
	}
	ref, ok := serviceRefPtr(ws, name)
	if !ok {
		return errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}
	applyOverride(ref, path, ref.LocalRef, "")
	return nil
}

// Unbind removes the named service's local-checkout (path) override, in
// memory only; SaveLocal persists it. A ref override, if any, is left
// alone: it was independent of the path override and becomes effective
// again now that the checkout no longer wins for reading. A service with no
// path override -- never bound, or overridden by ref alone -- is left
// alone, so an unbind is safe to repeat.
func Unbind(ws *domain.Workspace, name string) error {
	ref, ok := serviceRefPtr(ws, name)
	if !ok {
		return errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}
	if ref.Team == nil || ref.Source.Kind != domain.SourceLocal {
		return nil
	}
	team := *ref.Team
	if ref.LocalRef == "" {
		ref.Source = team
		ref.Team = nil
		return nil
	}
	src := team
	src.Ref = ref.LocalRef
	ref.Source = src
	return nil
}

// SetLocalRef sets this machine's ref override for name (sapien.workspace.
// local.yaml's `ref:`, PLAN §34f item 2), in memory only; SaveLocal
// persists it. A path override, if any, is preserved untouched: the
// checkout still wins for reading, and the ref becomes effective again if
// the path override is later removed (Unbind).
func SetLocalRef(ws *domain.Workspace, name, ref string) error {
	if ref == "" {
		return errs.New(errs.Invalid, "ref must not be empty").WithDetail("name", name)
	}
	r, ok := serviceRefPtr(ws, name)
	if !ok {
		return errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}
	path, contract := "", ""
	if r.Team != nil && r.Source.Kind == domain.SourceLocal {
		path = r.Source.Path
		if r.Source.Contract != r.Team.Contract {
			contract = r.Source.Contract
		}
	}
	applyOverride(r, path, ref, contract)
	return nil
}

// ClearLocalRef removes this machine's ref override for name, in memory
// only; SaveLocal persists it. A path override, if any, is left alone.
func ClearLocalRef(ws *domain.Workspace, name string) error {
	r, ok := serviceRefPtr(ws, name)
	if !ok {
		return errs.New(errs.ServiceNotFound, "service %q not found", name).WithDetail("name", name)
	}
	if r.LocalRef == "" {
		return errs.New(errs.Invalid, "service %q has no local ref override", name).WithDetail("name", name)
	}
	r.LocalRef = ""
	if r.Source.Kind != domain.SourceLocal {
		if r.Team != nil {
			r.Source = *r.Team
			r.Team = nil
		}
	}
	return nil
}

// EnsureLocalIgnored makes sure ws's root .gitignore lists the two things
// that belong to one machine, creating the file or appending what is
// missing:
//
//   - sapien.workspace.local.yaml names paths on one developer's disk, so
//     committing it would rebind every teammate's machine to directories
//     they do not have;
//   - .sapien/ holds the local index, daemon state, logs and this machine's
//     MCP permissions. Init also writes a "*" .gitignore inside it, but a
//     clone of a workspace repository creates .sapien/ on first open without
//     one, so the rule has to live in the committed root file.
//
// Init calls this for new workspaces, and bind and add for ones created
// before either rule existed. Idempotent: an entry already there, in any of
// its usual spellings (leading slash, trailing slash), is left as it is.
func EnsureLocalIgnored(ws *domain.Workspace) error {
	path := filepath.Join(ws.Dir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return errs.Wrap(errs.Internal, err, "reading %s", path)
	}

	present := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "/"), "/")
		present[line] = true
	}

	var missing []string
	for _, entry := range []string{domain.WorkspaceLocalFileName, domain.WorkspaceStateDir + "/"} {
		if !present[strings.TrimSuffix(entry, "/")] {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	var b strings.Builder
	b.Write(data)
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		b.WriteByte('\n')
	}
	for _, entry := range missing {
		b.WriteString(entry + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return errs.Wrap(errs.Internal, err, "writing %s", path)
	}
	return nil
}
