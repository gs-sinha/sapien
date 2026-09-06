package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/errs"
)

// defaultWorkspaceKey is the user-level config key naming the workspace
// every sapien command falls back to when none is discoverable from the
// current directory and --workspace / $SAPIEN_WORKSPACE are unset. It is
// what lets an agent run `sapien service sync <name>` from inside a
// service repo after `sapien mcp config --write` bound that workspace to
// the agent host: the CLI and the MCP server then agree on the workspace.
const defaultWorkspaceKey = "default_workspace"

// DefaultWorkspace returns the `default_workspace` directory from the
// user-level config file (UserPath), with a leading "~" expanded, or ""
// when the file or the key is absent.
func DefaultWorkspace() (string, error) {
	data, err := os.ReadFile(UserPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", errs.Wrap(errs.Internal, err, "reading %s", UserPath())
	}
	var doc struct {
		DefaultWorkspace string `yaml:"default_workspace"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", errs.Wrap(errs.Invalid, err, "parsing %s", UserPath())
	}
	return expandHome(strings.TrimSpace(doc.DefaultWorkspace)), nil
}

// SetDefaultWorkspace writes `default_workspace: <dir>` into the user-level
// config file, creating the file (0600, since the same file may later hold
// a semantic api_key) and its directory when needed and leaving every other
// top-level key untouched. dir is stored as given after filepath.Abs.
func SetDefaultWorkspace(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "resolving %q", dir)
	}
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
	doc[defaultWorkspaceKey] = abs

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

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
