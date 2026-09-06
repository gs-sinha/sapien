// Package workspace implements PLAN §7 (workspace specification): discovering,
// loading, saving, and initializing a Sapien workspace directory, plus the
// small helpers (state dir, service source resolution, environments) that sit
// directly on top of the on-disk layout.
package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// discoverHint is attached to every "no workspace found" error.
const discoverHint = "run `sapien init` or pass --workspace"

// Discover walks up from startDir looking for domain.WorkspaceFileName. It
// returns errs.WorkspaceNotFound if it reaches the filesystem root without
// finding one.
func Discover(startDir string) (*domain.Workspace, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "resolving start directory %q", startDir)
	}

	for {
		candidate := filepath.Join(dir, domain.WorkspaceFileName)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return Load(candidate)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return nil, errs.New(errs.WorkspaceNotFound, "no %s found from %s upward", domain.WorkspaceFileName, startDir).
		WithHint(discoverHint)
}

// Load reads and validates a sapien.workspace.yaml file, setting Dir and File
// on the result. It validates that version == 1 and that service names are
// unique, attaching a source line when one can be located in the YAML.
func Load(file string) (*domain.Workspace, error) {
	absFile, err := filepath.Abs(file)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "resolving workspace file path %q", file)
	}

	data, err := os.ReadFile(absFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errs.New(errs.WorkspaceNotFound, "workspace file not found: %s", absFile).
				WithHint(discoverHint)
		}
		return nil, errs.Wrap(errs.Internal, err, "reading workspace file %s", absFile)
	}

	var ws domain.Workspace
	if err := yaml.Unmarshal(data, &ws); err != nil {
		return nil, errs.Wrap(errs.Invalid, err, "parsing workspace file %s", absFile)
	}
	ws.Dir = filepath.Dir(absFile)
	ws.File = absFile

	// Best-effort node tree for source locations; a parse failure here would
	// already have been caught by the Unmarshal above, but this second parse
	// asks for line/column info that yaml.Unmarshal into a struct discards.
	var doc yaml.Node
	_ = yaml.Unmarshal(data, &doc)

	if ws.Version != 1 {
		e := errs.New(errs.Invalid, "unsupported workspace version %d (want 1)", ws.Version).
			WithDetail("file", absFile)
		if loc := findMappingKeyNode(&doc, "version"); loc != nil {
			e = e.WithSource(domain.SourceLoc{File: absFile, Line: loc.Line, Column: loc.Column})
		}
		return nil, e
	}

	seen := make(map[string]bool, len(ws.Services))
	for i, svc := range ws.Services {
		if svc.Name == "" {
			return nil, errs.New(errs.Invalid, "service at index %d has no name", i).
				WithDetail("file", absFile).WithDetail("index", i)
		}
		if seen[svc.Name] {
			e := errs.New(errs.Invalid, "duplicate service name %q", svc.Name).
				WithDetail("file", absFile).WithDetail("name", svc.Name)
			if loc := findServiceNameNode(&doc, i); loc != nil {
				e = e.WithSource(domain.SourceLoc{File: absFile, Line: loc.Line, Column: loc.Column})
			}
			return nil, e
		}
		seen[svc.Name] = true
	}

	return &ws, nil
}

// Save atomically writes ws back to ws.File (temp file + rename), with stable
// key order (Go struct field order) and 2-space indent.
func Save(ws *domain.Workspace) error {
	if ws.File == "" {
		return errs.New(errs.Invalid, "workspace has no File set")
	}
	data, err := encodeYAML(ws)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "encoding workspace")
	}
	return atomicWrite(ws.File, data)
}

// Init creates a new workspace rooted at dir: sapien.workspace.yaml,
// flows/, memories/, environments/local.yaml (a default, non-production
// local environment), and .sapien/ (with a .gitignore that ignores
// everything inside it). It returns errs.Conflict if a workspace already
// exists at dir.
func Init(dir, name string) (*domain.Workspace, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "resolving workspace directory %q", dir)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, errs.Wrap(errs.Internal, err, "creating workspace directory %s", absDir)
	}

	wsFile := filepath.Join(absDir, domain.WorkspaceFileName)
	if _, statErr := os.Stat(wsFile); statErr == nil {
		return nil, errs.New(errs.Conflict, "a workspace already exists at %s", wsFile)
	} else if !os.IsNotExist(statErr) {
		return nil, errs.Wrap(errs.Internal, statErr, "checking for existing workspace at %s", wsFile)
	}

	if name == "" {
		name = filepath.Base(absDir)
	}

	for _, sub := range []string{domain.FlowsDir, domain.MemoriesDir, domain.EnvironmentsDir} {
		if err := os.MkdirAll(filepath.Join(absDir, sub), 0o755); err != nil {
			return nil, errs.Wrap(errs.Internal, err, "creating %s", sub)
		}
	}

	stateDir := filepath.Join(absDir, domain.WorkspaceStateDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, errs.Wrap(errs.Internal, err, "creating %s", domain.WorkspaceStateDir)
	}
	if err := os.WriteFile(filepath.Join(stateDir, ".gitignore"), []byte("*\n"), 0o644); err != nil {
		return nil, errs.Wrap(errs.Internal, err, "writing %s/.gitignore", domain.WorkspaceStateDir)
	}

	ws := &domain.Workspace{
		Version: 1,
		Name:    name,
		Dir:     absDir,
		File:    wsFile,
	}

	localEnv := &domain.Environment{
		Version:    1,
		Name:       "local",
		Production: false,
		Path:       filepath.Join(absDir, domain.EnvironmentsDir, "local.yaml"),
	}
	if err := SaveEnvironment(ws, localEnv); err != nil {
		return nil, err
	}

	if err := Save(ws); err != nil {
		return nil, err
	}

	return ws, nil
}

// AddService appends ref to ws.Services, returning errs.Conflict if a service
// with the same name is already registered. It does not persist ws; call
// Save to write the change to disk.
func AddService(ws *domain.Workspace, ref domain.ServiceRef) error {
	for _, s := range ws.Services {
		if s.Name == ref.Name {
			return errs.New(errs.Conflict, "service %q already exists", ref.Name).WithDetail("name", ref.Name)
		}
	}
	ws.Services = append(ws.Services, ref)
	return nil
}

// RemoveService removes the named service from ws.Services, returning
// errs.ServiceNotFound if it is not registered. It does not persist ws; call
// Save to write the change to disk.
func RemoveService(ws *domain.Workspace, name string) error {
	for i, s := range ws.Services {
		if s.Name == name {
			ws.Services = append(ws.Services[:i], ws.Services[i+1:]...)
			return nil
		}
	}
	return errs.New(errs.ServiceNotFound, "service %q not found", name)
}

// ResolveSourcePath resolves a local source's path to an absolute path,
// expanding a leading "~" and resolving a relative path against ws.Dir. It
// returns errs.ServiceSource if the resolved path does not exist.
func ResolveSourcePath(ws *domain.Workspace, src domain.Source) (string, error) {
	if src.Kind != domain.SourceLocal {
		return "", errs.New(errs.Invalid, "ResolveSourcePath only supports local sources, got %q", src.Kind)
	}
	p := src.Path
	if p == "" {
		return "", errs.New(errs.ServiceSource, "local source has an empty path")
	}

	if p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errs.Wrap(errs.Internal, err, "resolving home directory")
		}
		p = home
	} else if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errs.Wrap(errs.Internal, err, "resolving home directory")
		}
		p = filepath.Join(home, p[2:])
	}

	if !filepath.IsAbs(p) {
		p = filepath.Join(ws.Dir, p)
	}

	abs, err := filepath.Abs(p)
	if err != nil {
		return "", errs.Wrap(errs.Internal, err, "resolving source path %q", p)
	}

	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return "", errs.New(errs.ServiceSource, "source path does not exist: %s", abs).WithDetail("path", abs)
		}
		return "", errs.Wrap(errs.Internal, err, "checking source path %s", abs)
	}

	return abs, nil
}

// StateDir returns the absolute path of ws's .sapien directory.
func StateDir(ws *domain.Workspace) string {
	return filepath.Join(ws.Dir, domain.WorkspaceStateDir)
}

// DBPath returns the absolute path of ws's SQLite database file.
func DBPath(ws *domain.Workspace) string {
	return filepath.Join(StateDir(ws), domain.WorkspaceDBFile)
}

// EnsureStateDir creates ws's .sapien directory if it does not already exist.
func EnsureStateDir(ws *domain.Workspace) error {
	if err := os.MkdirAll(StateDir(ws), 0o755); err != nil {
		return errs.Wrap(errs.Internal, err, "creating state directory %s", StateDir(ws))
	}
	return nil
}

// encodeYAML marshals v with a stable 2-space indent.
func encodeYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// atomicWrite writes data to path via a temp file in the same directory
// followed by a rename, so readers never observe a partial write.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errs.Wrap(errs.Internal, err, "creating directory %s", dir)
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return errs.Wrap(errs.Internal, err, "creating temp file in %s", dir)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errs.Wrap(errs.Internal, err, "writing temp file %s", tmpPath)
	}
	if err := tmp.Close(); err != nil {
		return errs.Wrap(errs.Internal, err, "closing temp file %s", tmpPath)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return errs.Wrap(errs.Internal, err, "renaming %s to %s", tmpPath, path)
	}
	return nil
}

// findMappingKeyNode returns the key node for a top-level mapping key in doc,
// used to attach a source line to an error. It returns nil if doc is not a
// single-document mapping or the key is absent.
func findMappingKeyNode(doc *yaml.Node, key string) *yaml.Node {
	root := documentRoot(doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i]
		}
	}
	return nil
}

// findServiceNameNode returns the "name" key node of the services[index]
// mapping entry, used to attach a source line to a duplicate-name error.
func findServiceNameNode(doc *yaml.Node, index int) *yaml.Node {
	root := documentRoot(doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	var services *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "services" {
			services = root.Content[i+1]
			break
		}
	}
	if services == nil || services.Kind != yaml.SequenceNode || index < 0 || index >= len(services.Content) {
		return nil
	}
	item := services.Content[index]
	if item.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(item.Content); i += 2 {
		if item.Content[i].Value == "name" {
			return item.Content[i]
		}
	}
	return nil
}

func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil || len(doc.Content) == 0 {
		return nil
	}
	return doc.Content[0]
}

// NormalizeLocalPath canonicalises a local source path for storage in the
// workspace file: an absolute path that lies inside ws.Dir is stored
// relative to it (so a committed workspace stays portable across
// checkouts), while paths outside the workspace, "~"-prefixed paths, and
// already-relative paths are returned unchanged. It never touches the
// filesystem; ResolveSourcePath does the resolving and existence check.
func NormalizeLocalPath(ws *domain.Workspace, p string) string {
	if ws == nil || ws.Dir == "" || p == "" || !filepath.IsAbs(p) {
		return p
	}
	clean := filepath.Clean(p)
	rel, err := filepath.Rel(ws.Dir, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return clean
	}
	if rel == "." {
		return "."
	}
	return rel
}
