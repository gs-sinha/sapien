package env

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

func TestResolve_Precedence(t *testing.T) {
	services := []domain.Service{
		{
			Name: "order-service",
			Environments: map[string]domain.EnvHint{
				"staging": {BaseURL: "https://orders.hint.internal/"},
			},
		},
		{
			Name: "allocation-service",
			Environments: map[string]domain.EnvHint{
				"staging": {BaseURL: "https://alloc.hint.internal"},
			},
		},
		{
			Name: "rider-service",
			// No service.yaml hint; only the fallback applies.
		},
		{
			Name: "no-url-service",
			// Nothing anywhere: must land in Missing.
		},
	}

	env := domain.Environment{
		Name: "staging",
		Services: map[string]domain.ServiceEnv{
			// env file wins over the service.yaml hint.
			"order-service": {BaseURL: "https://orders.staging.internal/"},
		},
	}

	fallback := map[string]string{
		"rider-service":      "https://rider.fallback.example",
		"allocation-service": "https://alloc.fallback.example", // should be ignored: hint wins
	}

	r := Resolve(env, services, fallback)

	assert.Equal(t, "https://orders.staging.internal", r.BaseURLs["order-service"], "env file base_url wins, trailing slash trimmed")
	assert.Equal(t, "https://alloc.hint.internal", r.BaseURLs["allocation-service"], "service.yaml hint wins over fallback")
	assert.Equal(t, "https://rider.fallback.example", r.BaseURLs["rider-service"], "fallback used when nothing else applies")
	assert.Equal(t, []string{"no-url-service"}, r.Missing)
}

func TestResolved_BaseURL(t *testing.T) {
	services := []domain.Service{{Name: "svc"}}
	env := domain.Environment{Name: "local"}
	r := Resolve(env, services, nil)

	_, err := r.BaseURL("svc")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	assert.Contains(t, errs.As(err).Hint, "environments/local.yaml")
	assert.Contains(t, errs.As(err).Hint, "sapien env scaffold")

	r2 := Resolve(env, services, map[string]string{"svc": "https://example.com"})
	url, err := r2.BaseURL("svc")
	require.NoError(t, err)
	assert.Equal(t, "https://example.com", url)
}

func TestResolved_AuthFor(t *testing.T) {
	serviceAuth := domain.Auth{Type: domain.AuthHeader, Name: "X-Api-Key", Value: "svc"}
	namedAuth := domain.Auth{Type: domain.AuthBearer, Token: "named"}
	defaultAuth := domain.Auth{Type: domain.AuthBearer, Token: "default"}

	env := domain.Environment{
		Name: "staging",
		Services: map[string]domain.ServiceEnv{
			"svc-with-override": {Auth: &serviceAuth},
		},
		Auth: map[string]domain.Auth{
			"named-svc": namedAuth,
			"default":   defaultAuth,
		},
	}
	r := Resolve(env, nil, nil)

	assert.Equal(t, &serviceAuth, r.AuthFor("svc-with-override"))
	assert.Equal(t, &namedAuth, r.AuthFor("named-svc"))
	assert.Equal(t, &defaultAuth, r.AuthFor("unlisted-svc"), "falls back to auth.default")

	noDefaultEnv := domain.Environment{Name: "local"}
	r2 := Resolve(noDefaultEnv, nil, nil)
	assert.Nil(t, r2.AuthFor("anything"))
}

func TestResolved_Transport_Defaults(t *testing.T) {
	env := domain.Environment{Name: "local"}
	r := Resolve(env, nil, nil)
	tr := r.Transport()

	assert.Equal(t, 30000, tr.TimeoutMs)
	assert.Equal(t, 10000, tr.ConnectTimeoutMs)
	assert.Equal(t, 5, tr.MaxRedirects)
	assert.Equal(t, int64(1<<20), tr.MaxBodyBytes)
	assert.False(t, tr.InsecureTLS)
}

func TestResolved_Transport_PartialOverride(t *testing.T) {
	env := domain.Environment{
		Name: "local",
		Transport: &domain.Transport{
			TimeoutMs:   5000,
			InsecureTLS: true,
		},
	}
	r := Resolve(env, nil, nil)
	tr := r.Transport()

	assert.Equal(t, 5000, tr.TimeoutMs, "explicit value preserved")
	assert.Equal(t, 10000, tr.ConnectTimeoutMs, "unset value filled with default")
	assert.Equal(t, 5, tr.MaxRedirects)
	assert.Equal(t, int64(1<<20), tr.MaxBodyBytes)
	assert.True(t, tr.InsecureTLS, "non-production environment honors insecure_tls")
}

func TestResolved_Transport_ProductionForcesSecureTLS(t *testing.T) {
	env := domain.Environment{
		Name:       "prod",
		Production: true,
		Transport:  &domain.Transport{InsecureTLS: true},
	}
	r := Resolve(env, nil, nil)
	tr := r.Transport()

	assert.False(t, tr.InsecureTLS, "production forces InsecureTLS off regardless of config")
}

func TestResolved_Var(t *testing.T) {
	env := domain.Environment{
		Name: "staging",
		Vars: map[string]string{"TEST_CUSTOMER": "cust_123"},
	}
	r := Resolve(env, nil, nil)

	v, ok := r.Var("TEST_CUSTOMER")
	assert.True(t, ok)
	assert.Equal(t, "cust_123", v)

	_, ok = r.Var("MISSING")
	assert.False(t, ok)
}
