package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/env"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/workspace"
)

func TestEnvs_ListGetDefault(t *testing.T) {
	ws, _ := setupWorkspace(t)
	require.NoError(t, workspace.SaveEnvironment(ws, &domain.Environment{Version: 1, Name: "staging"}))

	l, err := Open(ws, Options{Secrets: env.NewMemoryStore()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	envs, err := l.Envs().List(ctx)
	require.NoError(t, err)
	names := make([]string, 0, len(envs))
	for _, e := range envs {
		names = append(names, e.Name)
	}
	assert.Contains(t, names, "local")
	assert.Contains(t, names, "staging")

	got, err := l.Envs().Get(ctx, "staging")
	require.NoError(t, err)
	assert.Equal(t, "staging", got.Name)

	// Empty name resolves to the workspace default ("local", per
	// workspace.Init and workspace.DefaultEnvironment's fallback rule).
	def, err := l.Envs().Get(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, "local", def.Name)

	defName, err := l.Envs().Default(ctx)
	require.NoError(t, err)
	assert.Equal(t, "local", defName)
}

func TestEnvs_SetDefault(t *testing.T) {
	ws, _ := setupWorkspace(t)
	require.NoError(t, workspace.SaveEnvironment(ws, &domain.Environment{Version: 1, Name: "staging"}))

	l, err := Open(ws, Options{Secrets: env.NewMemoryStore()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	require.NoError(t, l.Envs().SetDefault(ctx, "staging"))
	name, err := l.Envs().Default(ctx)
	require.NoError(t, err)
	assert.Equal(t, "staging", name)

	// Persisted to sapien.workspace.yaml: reopening sees it too.
	require.NoError(t, l.Close())
	l2, err := Open(ws, Options{Secrets: env.NewMemoryStore()})
	require.NoError(t, err)
	defer l2.Close()
	name2, err := l2.Envs().Default(ctx)
	require.NoError(t, err)
	assert.Equal(t, "staging", name2)

	// Setting a nonexistent default is refused.
	err = l2.Envs().SetDefault(ctx, "does-not-exist")
	require.Error(t, err)
	assert.Equal(t, errs.EnvNotFound, errs.CodeOf(err))
}

func TestEnvs_SecretsRoundTrip(t *testing.T) {
	ws, _ := setupWorkspace(t)
	memStore := env.NewMemoryStore()

	l, err := Open(ws, Options{Secrets: memStore})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	names, err := l.Envs().ListSecrets(ctx)
	require.NoError(t, err)
	assert.Empty(t, names)

	require.NoError(t, l.Envs().SetSecret(ctx, "API_TOKEN", "s3cr3t"))
	require.NoError(t, l.Envs().SetSecret(ctx, "OTHER", "v"))

	names, err = l.Envs().ListSecrets(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"API_TOKEN", "OTHER"}, names)

	// Values are never returned by any API -- confirmed indirectly: the
	// injected memory store is what actually holds the value.
	v, err := memStore.Get("API_TOKEN")
	require.NoError(t, err)
	assert.Equal(t, "s3cr3t", v)

	require.NoError(t, l.Envs().DeleteSecret(ctx, "API_TOKEN"))
	names, err = l.Envs().ListSecrets(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"OTHER"}, names)
}

// declareStageHint adds a "stage" environment hint to order-service's
// service.yaml in fixDir (the temp copy setupWorkspace made, never the
// shared fixtures/logistics tree), so a subsequent Open sees it as a
// registered service's declared, but file-less, environment.
func declareStageHint(t *testing.T, fixDir string) {
	t.Helper()
	svcYAML := filepath.Join(fixDir, "order-service", "api", "service.yaml")
	data, err := os.ReadFile(svcYAML)
	require.NoError(t, err)
	const from = "environments:\n  local: { base_url: http://localhost:8081 }\n"
	const to = "environments:\n  local: { base_url: http://localhost:8081 }\n  stage: { base_url: http://order.stage.internal }\n"
	updated := strings.Replace(string(data), from, to, 1)
	require.NotEqual(t, string(data), updated, "fixtures/logistics/order-service/api/service.yaml changed shape; update this test")
	require.NoError(t, os.WriteFile(svcYAML, []byte(updated), 0o644))
}

func TestEnvs_Get_NotFound_ServicesDeclaringHint(t *testing.T) {
	ws, fixDir := setupWorkspace(t)
	declareStageHint(t, fixDir)

	l, err := Open(ws, Options{Secrets: env.NewMemoryStore()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Envs().Get(ctx, "stage")
	require.Error(t, err)
	e := errs.As(err)
	assert.Equal(t, errs.EnvNotFound, e.Code)
	assert.Contains(t, e.Hint, "available environments: local")
	assert.Contains(t, e.Hint, "services declaring stage: order-service")
	assert.Contains(t, e.Hint, "run `sapien env scaffold` to create environments/stage.yaml from their service.yaml hints")
	assert.Equal(t, []string{"order-service"}, e.Details["services_declaring"])
}

func TestEnvs_Get_NotFound_NoServiceDeclaresIt(t *testing.T) {
	ws, _ := setupWorkspace(t) // fixtures only declare "local"

	l, err := Open(ws, Options{Secrets: env.NewMemoryStore()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Envs().Get(ctx, "stage")
	require.Error(t, err)
	e := errs.As(err)
	assert.Equal(t, errs.EnvNotFound, e.Code)
	assert.Contains(t, e.Hint, "available environments: local")
	assert.NotContains(t, e.Hint, "services declaring", "no registered service declares stage")
}

func TestEnvs_Get_NotFound_EmptyNameNoDefault(t *testing.T) {
	ws, _ := setupWorkspace(t)
	// Blank out the configured default so Get("") hits the "no default
	// environment configured" branch, which has no specific name to look
	// declaring services up for.
	ws.DefaultEnvironment = ""
	require.NoError(t, os.Remove(filepath.Join(ws.Dir, domain.EnvironmentsDir, "local.yaml")))

	l, err := Open(ws, Options{Secrets: env.NewMemoryStore(), SkipStaleCheck: true})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Envs().Get(ctx, "")
	require.Error(t, err)
	e := errs.As(err)
	assert.Equal(t, errs.EnvNotFound, e.Code)
	assert.Contains(t, e.Message, "no default environment configured")
	assert.NotContains(t, e.Hint, "services declaring", "no specific name was requested, so nothing to look declarations up for")
}

func TestEnvs_SetDefault_NotFound_ServicesDeclaringHint(t *testing.T) {
	ws, fixDir := setupWorkspace(t)
	declareStageHint(t, fixDir)

	l, err := Open(ws, Options{Secrets: env.NewMemoryStore()})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	err = l.Envs().SetDefault(ctx, "stage")
	require.Error(t, err)
	e := errs.As(err)
	assert.Equal(t, errs.EnvNotFound, e.Code)
	assert.Contains(t, e.Hint, "services declaring stage: order-service")
	assert.Contains(t, e.Hint, "sapien env scaffold")
}

func TestEnvs_SetDefault_EmptyName(t *testing.T) {
	ws, _ := setupWorkspace(t)

	l, err := Open(ws, Options{Secrets: env.NewMemoryStore()})
	require.NoError(t, err)
	defer l.Close()

	err = l.Envs().SetDefault(context.Background(), "")
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}
