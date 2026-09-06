package env

import (
	"sort"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/workspace"
)

// MissingForService returns, sorted, every environment name svc declares
// in its service.yaml hints (svc.Environments) for which ws has no
// environments/<name>.yaml file yet. A workspace with no environments
// directory at all yields every name svc declares.
//
// A failure listing ws's environments is swallowed and treated as "none
// exist", the same as availableNames: this is advisory output (e.g.
// `sapien service add`'s summary), never a load path, so it must not fail
// the caller over it.
func MissingForService(ws *domain.Workspace, svc domain.Service) []string {
	have := map[string]bool{}
	if envs, err := workspace.ListEnvironments(ws); err == nil {
		for _, e := range envs {
			have[e.Name] = true
		}
	}

	var missing []string
	for name := range svc.Environments {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}
