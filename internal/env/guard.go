package env

import (
	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

// CheckProduction enforces the production guard (PLAN §20, §28): a
// production environment blocks execution unless the caller has explicitly
// allowed it (e.g. via the CLI's --allow-production flag).
func CheckProduction(env domain.Environment, allow bool) error {
	if env.Production && !allow {
		return errs.New(errs.ProductionBlocked, "environment %q is a production environment", env.Name).
			WithHint("pass --allow-production")
	}
	return nil
}
