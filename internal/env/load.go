package env

import (
	"fmt"
	"strings"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/workspace"
)

// availableNames lists the environments known to ws, for EnvNotFound
// Details; a failure listing them is swallowed since it must not mask the
// original not-found error.
func availableNames(ws *domain.Workspace) []string {
	envs, err := workspace.ListEnvironments(ws)
	if err != nil {
		return nil
	}
	names := make([]string, len(envs))
	for i, e := range envs {
		names[i] = e.Name
	}
	return names
}

// Load loads a workspace environment by name. An empty name resolves to
// workspace.DefaultEnvironment(ws). If the environment cannot be found (or
// no name was given and no default is configured), it returns
// errs.EnvNotFound with the available environment names in
// Details["available"].
func Load(ws *domain.Workspace, name string) (domain.Environment, error) {
	if name == "" {
		name = workspace.DefaultEnvironment(ws)
	}
	if name == "" {
		return domain.Environment{}, errs.New(errs.EnvNotFound, "no default environment configured").
			WithHint("run `sapien env list`, then pass --env NAME or set default_environment").
			WithDetail("available", availableNames(ws))
	}

	loaded, err := workspace.LoadEnvironment(ws, name)
	if err != nil {
		if e := errs.As(err); e.Code == errs.EnvNotFound {
			available := availableNames(ws)
			e.WithDetail("available", available)
			e.WithHint(notFoundHint(available))
		}
		return domain.Environment{}, err
	}
	return *loaded, nil
}

// notFoundHint is the base hint for an environment name that does not
// exist: the workspace's other environments, if any, so a typo or wrong
// --env is obvious without a separate `sapien env list` round trip.
func notFoundHint(available []string) string {
	if len(available) == 0 {
		return "no environments exist yet; run `sapien env list` to confirm, or create environments/<name>.yaml"
	}
	return fmt.Sprintf("available environments: %s (run `sapien env list` for details)", strings.Join(available, ", "))
}

// List returns every environment defined in ws, sorted by name.
func List(ws *domain.Workspace) ([]domain.Environment, error) {
	return workspace.ListEnvironments(ws)
}
