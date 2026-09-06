package env

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// Default transport settings applied when an environment does not specify
// them (PLAN §7).
const (
	defaultTimeoutMs        = 30000
	defaultConnectTimeoutMs = 10000
	defaultMaxRedirects     = 5
	defaultMaxBodyBytes     = 1 << 20
)

// Resolved is an environment merged with a set of known services, ready to
// drive requests: base URLs, auth, and transport settings.
type Resolved struct {
	Env      domain.Environment
	BaseURLs map[string]string // service name -> base URL, trailing slash trimmed
	Missing  []string          // services with no base URL from any source, sorted
}

// Resolve merges base URLs for services with precedence (PLAN §7):
//  1. env.Services[svc].base_url (the environment file)
//  2. svc.Environments[env.Name].base_url (the service's service.yaml)
//  3. fallback[svc] (caller-provided, e.g. the OpenAPI document's servers[0])
//
// A service with no base URL from any source is recorded in Missing rather
// than causing an error, since a workspace may reasonably have services it
// hasn't configured yet for a given environment.
func Resolve(env domain.Environment, services []domain.Service, fallback map[string]string) *Resolved {
	r := &Resolved{Env: env, BaseURLs: map[string]string{}}
	for _, svc := range services {
		base := ""
		if se, ok := env.Services[svc.Name]; ok && se.BaseURL != "" {
			base = se.BaseURL
		} else if hint, ok := svc.Environments[env.Name]; ok && hint.BaseURL != "" {
			base = hint.BaseURL
		} else if fb, ok := fallback[svc.Name]; ok && fb != "" {
			base = fb
		}

		if base == "" {
			r.Missing = append(r.Missing, svc.Name)
			continue
		}
		r.BaseURLs[svc.Name] = strings.TrimRight(base, "/")
	}
	sort.Strings(r.Missing)
	return r
}

// BaseURL returns the resolved base URL for service, or errs.Invalid if none
// was resolved.
func (r *Resolved) BaseURL(service string) (string, error) {
	if url, ok := r.BaseURLs[service]; ok {
		return url, nil
	}
	return "", errs.New(errs.Invalid, "no base_url for service %s in environment %s", service, r.Env.Name).
		WithHint(fmt.Sprintf("add services.%s.base_url to environments/%s.yaml, or run `sapien env scaffold`", service, r.Env.Name))
}

// AuthFor returns the auth rule that applies to service, with precedence
// (PLAN §7):
//  1. env.Services[service].auth
//  2. env.Auth[service]
//  3. env.Auth["default"]
//  4. nil (no auth)
func (r *Resolved) AuthFor(service string) *domain.Auth {
	if se, ok := r.Env.Services[service]; ok && se.Auth != nil {
		return se.Auth
	}
	if a, ok := r.Env.Auth[service]; ok {
		return &a
	}
	if a, ok := r.Env.Auth["default"]; ok {
		return &a
	}
	return nil
}

// Transport returns the environment's transport settings with defaults
// filled in for any zero-valued field. InsecureTLS is forced false when the
// environment is a production environment, regardless of what was
// configured (PLAN §28).
func (r *Resolved) Transport() domain.Transport {
	t := domain.Transport{}
	if r.Env.Transport != nil {
		t = *r.Env.Transport
	}
	if t.TimeoutMs == 0 {
		t.TimeoutMs = defaultTimeoutMs
	}
	if t.ConnectTimeoutMs == 0 {
		t.ConnectTimeoutMs = defaultConnectTimeoutMs
	}
	if t.MaxRedirects == 0 {
		t.MaxRedirects = defaultMaxRedirects
	}
	if t.MaxBodyBytes == 0 {
		t.MaxBodyBytes = defaultMaxBodyBytes
	}
	if r.Env.Production {
		t.InsecureTLS = false
	}
	return t
}

// Var returns the value of a workspace variable declared in the
// environment's vars block.
func (r *Resolved) Var(name string) (string, bool) {
	v, ok := r.Env.Vars[name]
	return v, ok
}
