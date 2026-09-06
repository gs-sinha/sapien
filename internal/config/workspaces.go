package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// workspacesKey is the user-level config key listing every workspace this
// machine knows about. It is a convenience index, never a source of truth:
// a workspace is a directory with a sapien.workspace.yaml in it, and one
// that is missing from this list still works exactly as before when named
// by --workspace, $SAPIEN_WORKSPACE, or an upward search. The list is what
// lets `sapien workspace list`, the UI's picker, and MCP's list_workspaces
// offer a choice without the caller already knowing every path.
const workspacesKey = "workspaces"

// KnownWorkspaces returns the registered workspace directories from the
// user-level config file, "~"-expanded and de-duplicated, plus the default
// workspace (which is always considered registered even if it was never
// added explicitly). Returns an empty slice when the file or key is absent.
func KnownWorkspaces() ([]string, error) {
	dirs, err := readWorkspaceList()
	if err != nil {
		return nil, err
	}

	def, err := DefaultWorkspace()
	if err != nil {
		return nil, err
	}
	if def != "" {
		dirs = append(dirs, def)
	}

	return dedupePaths(dirs), nil
}

// AddWorkspace registers dir (absolute) in the user-level config, leaving
// every other key untouched. Registering an already-registered workspace is
// a no-op, so callers may register opportunistically on every open.
func AddWorkspace(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "resolving %q", dir)
	}

	existing, err := readWorkspaceList()
	if err != nil {
		return err
	}
	for _, d := range existing {
		if workspace.SameDir(d, abs) {
			return nil
		}
	}

	return writeWorkspaceList(append(existing, abs))
}

// RemoveWorkspace unregisters dir. The workspace directory itself is never
// touched: this only forgets the path, so it stops being offered as a
// choice. Removing one that is not registered is a no-op.
func RemoveWorkspace(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "resolving %q", dir)
	}

	existing, err := readWorkspaceList()
	if err != nil {
		return err
	}
	kept := make([]string, 0, len(existing))
	for _, d := range existing {
		if !workspace.SameDir(d, abs) {
			kept = append(kept, d)
		}
	}
	if len(kept) == len(existing) {
		return nil
	}
	return writeWorkspaceList(kept)
}

func readWorkspaceList() ([]string, error) {
	data, err := os.ReadFile(UserPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errs.Wrap(errs.Internal, err, "reading %s", UserPath())
	}
	var doc struct {
		Workspaces []string `yaml:"workspaces"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, errs.Wrap(errs.Invalid, err, "parsing %s", UserPath())
	}

	out := make([]string, 0, len(doc.Workspaces))
	for _, d := range doc.Workspaces {
		if d = expandHome(strings.TrimSpace(d)); d != "" {
			out = append(out, d)
		}
	}
	return out, nil
}

// writeWorkspaceList rewrites just the `workspaces` key, preserving every
// other top-level key in the user config (the same merge SetDefaultWorkspace
// does -- this file may also hold semantic.api_key, git, daemon, mcp).
func writeWorkspaceList(dirs []string) error {
	path := UserPath()

	doc := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if strings.TrimSpace(string(data)) != "" {
			if err := yaml.Unmarshal(data, &doc); err != nil {
				return errs.Wrap(errs.Invalid, err, "parsing %s", path)
			}
		}
	case os.IsNotExist(err):
	default:
		return errs.Wrap(errs.Internal, err, "reading %s", path)
	}

	sorted := dedupePaths(dirs)
	if len(sorted) == 0 {
		delete(doc, workspacesKey)
	} else {
		doc[workspacesKey] = sorted
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "encoding %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errs.Wrap(errs.Internal, err, "creating %s", filepath.Dir(path))
	}
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, out, mode); err != nil {
		return errs.Wrap(errs.Internal, err, "writing %s", path)
	}
	return nil
}

// dedupePaths cleans, de-duplicates, and sorts paths for a stable list.
//
// De-duplication is by directory, not by string: on a case-insensitive
// filesystem "~/Desktop/ws" and "~/desktop/ws" are one workspace, and
// listing both makes a phantom extra workspace appear in every picker. The
// list is sorted first so which spelling survives is deterministic rather
// than dependent on the order things were registered.
func dedupePaths(dirs []string) []string {
	cleaned := make([]string, 0, len(dirs))
	seen := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		if d == "" {
			continue
		}
		c := filepath.Clean(d)
		if seen[c] {
			continue
		}
		seen[c] = true
		cleaned = append(cleaned, c)
	}
	sort.Strings(cleaned)

	out := make([]string, 0, len(cleaned))
	for _, c := range cleaned {
		dup := false
		for _, kept := range out {
			if workspace.SameDir(kept, c) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, c)
		}
	}
	return out
}
