package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// environmentsDir returns the absolute path of ws's environments directory.
func environmentsDir(ws *domain.Workspace) string {
	return filepath.Join(ws.Dir, domain.EnvironmentsDir)
}

// ListEnvironments reads every environments/*.yaml file, setting Path on
// each and sorting the result by name. A missing environments directory is
// not an error; it yields an empty (nil) slice.
func ListEnvironments(ws *domain.Workspace) ([]domain.Environment, error) {
	dir := environmentsDir(ws)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errs.Wrap(errs.Internal, err, "reading environments directory %s", dir)
	}

	var envs []domain.Environment
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		env, err := readEnvironmentFile(path)
		if err != nil {
			return nil, err
		}
		envs = append(envs, *env)
	}

	sort.Slice(envs, func(i, j int) bool { return envs[i].Name < envs[j].Name })
	return envs, nil
}

// LoadEnvironment reads environments/<name>.yaml, returning
// errs.EnvNotFound if it does not exist.
func LoadEnvironment(ws *domain.Workspace, name string) (*domain.Environment, error) {
	path := filepath.Join(environmentsDir(ws), name+".yaml")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, errs.New(errs.EnvNotFound, "environment %q not found", name).
				WithHint("run `sapien env list`")
		}
		return nil, errs.Wrap(errs.Internal, err, "checking environment file %s", path)
	}
	return readEnvironmentFile(path)
}

// SaveEnvironment atomically writes env to environments/<name>.yaml (or to
// env.Path if already set), with 2-space indent.
func SaveEnvironment(ws *domain.Workspace, env *domain.Environment) error {
	if env.Path == "" {
		env.Path = filepath.Join(environmentsDir(ws), env.Name+".yaml")
	}
	data, err := encodeYAML(env)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "encoding environment %s", env.Name)
	}
	return atomicWrite(env.Path, data)
}

// DefaultEnvironment returns the name of ws's default environment:
// ws.DefaultEnvironment if set, else "local" if environments/local.yaml
// exists, else the name of the only environment if there is exactly one,
// else "".
func DefaultEnvironment(ws *domain.Workspace) string {
	if ws.DefaultEnvironment != "" {
		return ws.DefaultEnvironment
	}
	if _, err := os.Stat(filepath.Join(environmentsDir(ws), "local.yaml")); err == nil {
		return "local"
	}
	envs, err := ListEnvironments(ws)
	if err == nil && len(envs) == 1 {
		return envs[0].Name
	}
	return ""
}

// readEnvironmentFile loads a single environment file that is already known
// (or assumed) to exist.
func readEnvironmentFile(path string) (*domain.Environment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errs.New(errs.EnvNotFound, "environment file not found: %s", path)
		}
		return nil, errs.Wrap(errs.Internal, err, "reading environment file %s", path)
	}

	var env domain.Environment
	if err := yaml.Unmarshal(data, &env); err != nil {
		return nil, errs.Wrap(errs.Invalid, err, "parsing environment file %s", path)
	}
	env.Path = path
	return &env, nil
}
