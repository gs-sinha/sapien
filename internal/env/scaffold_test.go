package env

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
)

func TestLooksProduction(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"prod", true},
		{"Production", true},
		{"prod-eu", true},
		{"PROD", true},
		{"canary-production", true},
		{"local", false},
		{"staging", false},
		{"stage", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, looksProduction(tc.name))
		})
	}
}

func TestScaffold_CreatesMissingFile(t *testing.T) {
	ws := testWorkspace(t)
	services := []domain.Service{
		{Name: "order-service", Environments: map[string]domain.EnvHint{
			"stage": {BaseURL: "http://order.stage.internal"},
		}},
		{Name: "rider-service", Environments: map[string]domain.EnvHint{
			"stage": {BaseURL: "http://rider.stage.internal"},
		}},
	}

	report, err := Scaffold(ws, services, false)
	require.NoError(t, err)
	require.True(t, report.Changed())
	require.Len(t, report.Files, 1)

	f := report.Files[0]
	assert.Equal(t, "stage", f.Environment)
	assert.True(t, f.Created)
	assert.Equal(t, filepath.Join(ws.Dir, "environments", "stage.yaml"), f.Path)
	require.Len(t, f.Entries, 2)
	assert.Equal(t, "order-service", f.Entries[0].Service)
	assert.Equal(t, "http://order.stage.internal", f.Entries[0].BaseURL)
	assert.False(t, f.Entries[0].Overwritten)
	assert.Equal(t, "rider-service", f.Entries[1].Service)

	// Persisted correctly on disk.
	saved, err := Load(ws, "stage")
	require.NoError(t, err)
	assert.False(t, saved.Production)
	assert.Equal(t, "http://order.stage.internal", saved.Services["order-service"].BaseURL)
	assert.Equal(t, "http://rider.stage.internal", saved.Services["rider-service"].BaseURL)
}

func TestScaffold_ProductionNameDefaultsProductionTrue(t *testing.T) {
	ws := testWorkspace(t)
	services := []domain.Service{
		{Name: "order-service", Environments: map[string]domain.EnvHint{
			"production": {BaseURL: "https://order.prod.internal"},
		}},
	}

	_, err := Scaffold(ws, services, false)
	require.NoError(t, err)

	saved, err := Load(ws, "production")
	require.NoError(t, err)
	assert.True(t, saved.Production)
}

func TestScaffold_ExistingFile_AddsMissingEntry(t *testing.T) {
	ws := testWorkspace(t)
	writeEnvFile(t, ws, "stage", "version: 1\nname: stage\nvars:\n  REGION: eu\nservices:\n  order-service:\n    base_url: http://order.stage.internal\n")

	services := []domain.Service{
		{Name: "order-service", Environments: map[string]domain.EnvHint{
			"stage": {BaseURL: "http://order.stage.internal"},
		}},
		{Name: "rider-service", Environments: map[string]domain.EnvHint{
			"stage": {BaseURL: "http://rider.stage.internal"},
		}},
	}

	report, err := Scaffold(ws, services, false)
	require.NoError(t, err)
	require.Len(t, report.Files, 1)

	f := report.Files[0]
	assert.False(t, f.Created)
	require.Len(t, f.Entries, 1, "order-service already had a base_url; only rider-service should be added")
	assert.Equal(t, "rider-service", f.Entries[0].Service)
	assert.Equal(t, "http://rider.stage.internal", f.Entries[0].BaseURL)

	saved, err := Load(ws, "stage")
	require.NoError(t, err)
	assert.Equal(t, "eu", saved.Vars["REGION"], "unrelated keys preserved")
	assert.Equal(t, "http://order.stage.internal", saved.Services["order-service"].BaseURL, "existing base_url untouched")
	assert.Equal(t, "http://rider.stage.internal", saved.Services["rider-service"].BaseURL)
}

func TestScaffold_NoForce_LeavesConflictingBaseURLAlone(t *testing.T) {
	ws := testWorkspace(t)
	writeEnvFile(t, ws, "stage", "version: 1\nname: stage\nservices:\n  order-service:\n    base_url: http://old.example.com\n")

	services := []domain.Service{
		{Name: "order-service", Environments: map[string]domain.EnvHint{
			"stage": {BaseURL: "http://new.example.com"},
		}},
	}

	report, err := Scaffold(ws, services, false)
	require.NoError(t, err)
	assert.False(t, report.Changed(), "no --force: an existing, different base_url must not change, and nothing else needed a change")

	saved, err := Load(ws, "stage")
	require.NoError(t, err)
	assert.Equal(t, "http://old.example.com", saved.Services["order-service"].BaseURL)
}

func TestScaffold_Force_OverwritesConflictingBaseURL(t *testing.T) {
	ws := testWorkspace(t)
	writeEnvFile(t, ws, "stage", "version: 1\nname: stage\nservices:\n  order-service:\n    base_url: http://old.example.com\n")

	services := []domain.Service{
		{Name: "order-service", Environments: map[string]domain.EnvHint{
			"stage": {BaseURL: "http://new.example.com"},
		}},
	}

	report, err := Scaffold(ws, services, true)
	require.NoError(t, err)
	require.True(t, report.Changed())
	require.Len(t, report.Files[0].Entries, 1)
	assert.True(t, report.Files[0].Entries[0].Overwritten)

	saved, err := Load(ws, "stage")
	require.NoError(t, err)
	assert.Equal(t, "http://new.example.com", saved.Services["order-service"].BaseURL)
}

func TestScaffold_EmptyExistingBaseURL_IsFilledInWithoutForce(t *testing.T) {
	ws := testWorkspace(t)
	writeEnvFile(t, ws, "stage", "version: 1\nname: stage\nservices:\n  order-service: {}\n")

	services := []domain.Service{
		{Name: "order-service", Environments: map[string]domain.EnvHint{
			"stage": {BaseURL: "http://order.stage.internal"},
		}},
	}

	report, err := Scaffold(ws, services, false)
	require.NoError(t, err)
	require.Len(t, report.Files, 1)
	require.Len(t, report.Files[0].Entries, 1)
	assert.False(t, report.Files[0].Entries[0].Overwritten, "filling an empty base_url is not an overwrite")

	saved, err := Load(ws, "stage")
	require.NoError(t, err)
	assert.Equal(t, "http://order.stage.internal", saved.Services["order-service"].BaseURL)
}

func TestScaffold_PreservesAuthOnExistingServiceEntry(t *testing.T) {
	ws := testWorkspace(t)
	writeEnvFile(t, ws, "stage", "version: 1\nname: stage\nservices:\n  order-service:\n    auth:\n      type: bearer\n      token: \"${secret.X}\"\n")

	services := []domain.Service{
		{Name: "order-service", Environments: map[string]domain.EnvHint{
			"stage": {BaseURL: "http://order.stage.internal"},
		}},
	}

	_, err := Scaffold(ws, services, false)
	require.NoError(t, err)

	saved, err := Load(ws, "stage")
	require.NoError(t, err)
	se := saved.Services["order-service"]
	assert.Equal(t, "http://order.stage.internal", se.BaseURL)
	require.NotNil(t, se.Auth)
	assert.Equal(t, domain.AuthBearer, se.Auth.Type)
}

func TestScaffold_NothingNeeded(t *testing.T) {
	ws := testWorkspace(t)
	writeEnvFile(t, ws, "stage", "version: 1\nname: stage\nservices:\n  order-service:\n    base_url: http://order.stage.internal\n")

	services := []domain.Service{
		{Name: "order-service", Environments: map[string]domain.EnvHint{
			"stage": {BaseURL: "http://order.stage.internal"},
		}},
	}

	report, err := Scaffold(ws, services, false)
	require.NoError(t, err)
	assert.False(t, report.Changed())
	assert.Empty(t, report.Files)
}

func TestScaffold_NoHints_NoOp(t *testing.T) {
	ws := testWorkspace(t)
	services := []domain.Service{{Name: "order-service"}} // no Environments hints at all
	report, err := Scaffold(ws, services, false)
	require.NoError(t, err)
	assert.False(t, report.Changed())
}

func TestScaffold_EmptyHintBaseURL_Skipped(t *testing.T) {
	ws := testWorkspace(t)
	services := []domain.Service{
		{Name: "order-service", Environments: map[string]domain.EnvHint{"stage": {BaseURL: ""}}},
	}
	report, err := Scaffold(ws, services, false)
	require.NoError(t, err)
	assert.False(t, report.Changed())

	_, err = os.Stat(filepath.Join(ws.Dir, "environments", "stage.yaml"))
	assert.True(t, os.IsNotExist(err))
}

func TestScaffold_MultipleEnvironments_SortedOrder(t *testing.T) {
	ws := testWorkspace(t)
	services := []domain.Service{
		{Name: "order-service", Environments: map[string]domain.EnvHint{
			"zzz-stage": {BaseURL: "http://order.zzz.internal"},
			"aaa-stage": {BaseURL: "http://order.aaa.internal"},
		}},
	}
	report, err := Scaffold(ws, services, false)
	require.NoError(t, err)
	require.Len(t, report.Files, 2)
	assert.Equal(t, "aaa-stage", report.Files[0].Environment)
	assert.Equal(t, "zzz-stage", report.Files[1].Environment)
}

func TestScaffold_LoadErrorOtherThanNotFound_Propagates(t *testing.T) {
	ws := testWorkspace(t)
	// A directory in place of environments/stage.yaml makes the read fail
	// with something other than EnvNotFound.
	dir := filepath.Join(ws.Dir, "environments", "stage.yaml")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	services := []domain.Service{
		{Name: "order-service", Environments: map[string]domain.EnvHint{
			"stage": {BaseURL: "http://order.stage.internal"},
		}},
	}
	_, err := Scaffold(ws, services, false)
	require.Error(t, err)
}
