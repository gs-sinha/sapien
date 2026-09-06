package env

import (
	"regexp"
	"sort"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// prodNamePattern matches an environment name that should default to
// production: true when Scaffold creates its file for the first time.
var prodNamePattern = regexp.MustCompile(`(?i)^prod`)

// looksProduction reports whether name looks like a production
// environment: it starts with "prod" (case-insensitive) or contains
// "production" anywhere.
func looksProduction(name string) bool {
	if prodNamePattern.MatchString(name) {
		return true
	}
	return strings.Contains(strings.ToLower(name), "production")
}

// ScaffoldEntry is one service base_url Scaffold wrote into an environment
// file.
type ScaffoldEntry struct {
	Service string `json:"service"`
	BaseURL string `json:"base_url"`
	// Overwritten is true when this entry replaced an existing, different
	// base_url (only possible when Scaffold was called with force).
	Overwritten bool `json:"overwritten,omitempty"`
}

// ScaffoldFile is one environments/<name>.yaml file Scaffold created or
// added entries to.
type ScaffoldFile struct {
	Environment string          `json:"environment"`
	Path        string          `json:"path"`
	Created     bool            `json:"created"`
	Entries     []ScaffoldEntry `json:"entries"`
}

// Report is Scaffold's result: one ScaffoldFile per environment file it
// created or changed, in environment-name order. An empty Report means
// every environment any service declares already had a file with a
// base_url on record for that service.
type Report struct {
	Files []ScaffoldFile `json:"files"`
}

// Changed reports whether Scaffold made any change at all.
func (r Report) Changed() bool { return len(r.Files) > 0 }

// Scaffold creates or updates environments/<name>.yaml for every
// environment name any of services declares in its service.yaml hints
// (svc.Environments), so a workspace with services registered against
// environments nobody has ever hand-written a file for gets one
// automatically (PLAN §20: service.yaml environments are only hints; the
// workspace env file is what actually decides, and previously nothing
// created it).
//
// For each (environment, service) pair a service declares a hint for:
//   - a missing environments/<name>.yaml is created with version: 1, the
//     name, production set from the name (see looksProduction), and a
//     services entry for every service that declares that environment;
//   - an existing file with no entry for the service, or an entry with an
//     empty base_url, gets one added;
//   - an existing, non-empty base_url is left untouched unless force is
//     true, in which case it is overwritten with the service.yaml hint.
//
// Every other field of an existing environment file (vars, auth,
// transport, redaction, entries for services with no hint) is preserved
// untouched: Scaffold loads the file, mutates the Services map, and saves
// it back via workspace.SaveEnvironment.
func Scaffold(ws *domain.Workspace, services []domain.Service, force bool) (Report, error) {
	// envName -> serviceName -> base_url, collected from every service's
	// service.yaml hints.
	hints := map[string]map[string]string{}
	for _, svc := range services {
		for envName, hint := range svc.Environments {
			if hint.BaseURL == "" {
				continue
			}
			if hints[envName] == nil {
				hints[envName] = map[string]string{}
			}
			hints[envName][svc.Name] = hint.BaseURL
		}
	}

	envNames := make([]string, 0, len(hints))
	for name := range hints {
		envNames = append(envNames, name)
	}
	sort.Strings(envNames)

	var report Report
	for _, envName := range envNames {
		file, err := scaffoldOne(ws, envName, hints[envName], force)
		if err != nil {
			return Report{}, err
		}
		if file != nil {
			report.Files = append(report.Files, *file)
		}
	}
	return report, nil
}

// scaffoldOne creates or updates a single environments/<name>.yaml from
// svcBaseURLs (serviceName -> base_url), returning nil when nothing
// changed.
func scaffoldOne(ws *domain.Workspace, envName string, svcBaseURLs map[string]string, force bool) (*ScaffoldFile, error) {
	loaded, err := workspace.LoadEnvironment(ws, envName)
	created := false
	if err != nil {
		if errs.CodeOf(err) != errs.EnvNotFound {
			return nil, err
		}
		loaded = &domain.Environment{
			Version:    1,
			Name:       envName,
			Production: looksProduction(envName),
		}
		created = true
	}

	if loaded.Services == nil {
		loaded.Services = map[string]domain.ServiceEnv{}
	}

	svcNames := make([]string, 0, len(svcBaseURLs))
	for name := range svcBaseURLs {
		svcNames = append(svcNames, name)
	}
	sort.Strings(svcNames)

	var entries []ScaffoldEntry
	for _, svcName := range svcNames {
		baseURL := svcBaseURLs[svcName]
		existing, ok := loaded.Services[svcName]
		switch {
		case !ok || existing.BaseURL == "":
			existing.BaseURL = baseURL
			loaded.Services[svcName] = existing
			entries = append(entries, ScaffoldEntry{Service: svcName, BaseURL: baseURL})
		case force && existing.BaseURL != baseURL:
			existing.BaseURL = baseURL
			loaded.Services[svcName] = existing
			entries = append(entries, ScaffoldEntry{Service: svcName, BaseURL: baseURL, Overwritten: true})
		}
	}

	if !created && len(entries) == 0 {
		return nil, nil
	}

	if err := workspace.SaveEnvironment(ws, loaded); err != nil {
		return nil, err
	}

	return &ScaffoldFile{
		Environment: envName,
		Path:        loaded.Path,
		Created:     created,
		Entries:     entries,
	}, nil
}
