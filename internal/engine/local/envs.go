package local

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/env"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// envAPI implements engine.EnvAPI over a Local (PLAN §7, §20).
type envAPI struct{ l *Local }

var _ engine.EnvAPI = (*envAPI)(nil)

func (e *envAPI) List(ctx context.Context) ([]domain.Environment, error) {
	return env.List(e.l.ws)
}

// Get loads name, or the workspace default when name == "".
func (e *envAPI) Get(ctx context.Context, name string) (*domain.Environment, error) {
	loaded, err := env.Load(e.l.ws, name)
	if err != nil {
		lookup := name
		if lookup == "" {
			lookup = workspace.DefaultEnvironment(e.l.ws)
		}
		return nil, e.enrichEnvNotFound(ctx, err, lookup)
	}
	return &loaded, nil
}

// enrichEnvNotFound appends a "services declaring <name>" note, with the
// `sapien env scaffold` suggestion, to an EnvNotFound error's hint when
// any registered service's service.yaml declares an environment hint for
// name (PLAN §20 feedback: environments are the weakest part of the
// onboarding story otherwise -- a workspace can have services expecting an
// environment that nobody has ever created a file for, and nothing says
// so until a run fails). err is returned unchanged when it is not
// EnvNotFound, name is empty (nothing to look up), or the service list
// itself cannot be read (a lookup failure must never mask the original
// not-found error).
func (e *envAPI) enrichEnvNotFound(ctx context.Context, err error, name string) error {
	ee := errs.As(err)
	if ee.Code != errs.EnvNotFound || name == "" {
		return err
	}
	svcs, svcErr := e.l.Services().List(ctx)
	if svcErr != nil {
		return err
	}
	var declaring []string
	for _, svc := range svcs {
		if _, ok := svc.Environments[name]; ok {
			declaring = append(declaring, svc.Name)
		}
	}
	if len(declaring) == 0 {
		return err
	}
	sort.Strings(declaring)
	ee.WithHint(fmt.Sprintf("%s; services declaring %s: %s; run `sapien env scaffold` to create environments/%s.yaml from their service.yaml hints",
		ee.Hint, name, strings.Join(declaring, ", "), name))
	ee.WithDetail("services_declaring", declaring)
	return ee
}

func (e *envAPI) Default(ctx context.Context) (string, error) {
	name := workspace.DefaultEnvironment(e.l.ws)
	if name == "" {
		return "", errs.New(errs.EnvNotFound, "no default environment configured").
			WithHint("run `sapien env list`, then set default_environment")
	}
	return name, nil
}

// SetDefault requires name to already exist and persists it as
// ws.DefaultEnvironment.
func (e *envAPI) SetDefault(ctx context.Context, name string) error {
	if name == "" {
		return errs.New(errs.Invalid, "environment name is required").
			WithHint("pass a name, e.g. `sapien env use local`")
	}
	if _, err := workspace.LoadEnvironment(e.l.ws, name); err != nil {
		return e.enrichEnvNotFound(ctx, err, name)
	}
	e.l.ws.DefaultEnvironment = name
	return workspace.Save(e.l.ws)
}

// Secrets: values are never returned by any API (PLAN §20, §28).

func (e *envAPI) SetSecret(ctx context.Context, name, value string) error {
	return e.l.secrets.Set(name, value)
}

func (e *envAPI) ListSecrets(ctx context.Context) ([]string, error) {
	return e.l.secrets.List()
}

func (e *envAPI) DeleteSecret(ctx context.Context, name string) error {
	return e.l.secrets.Delete(name)
}
