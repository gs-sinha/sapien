package env

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/errs"
)

func TestLoad_ExplicitName(t *testing.T) {
	ws := testWorkspace(t)
	writeEnvFile(t, ws, "staging", "version: 1\nname: staging\nproduction: false\n")

	got, err := Load(ws, "staging")
	require.NoError(t, err)
	assert.Equal(t, "staging", got.Name)
}

func TestLoad_DefaultName_FromWorkspaceSetting(t *testing.T) {
	ws := testWorkspace(t)
	ws.DefaultEnvironment = "staging"
	writeEnvFile(t, ws, "staging", "version: 1\nname: staging\n")
	writeEnvFile(t, ws, "local", "version: 1\nname: local\n")

	got, err := Load(ws, "")
	require.NoError(t, err)
	assert.Equal(t, "staging", got.Name)
}

func TestLoad_DefaultName_FallsBackToLocal(t *testing.T) {
	ws := testWorkspace(t)
	writeEnvFile(t, ws, "local", "version: 1\nname: local\n")

	got, err := Load(ws, "")
	require.NoError(t, err)
	assert.Equal(t, "local", got.Name)
}

func TestLoad_NoDefaultConfigured(t *testing.T) {
	ws := testWorkspace(t)
	writeEnvFile(t, ws, "staging", "version: 1\nname: staging\n")
	writeEnvFile(t, ws, "prod", "version: 1\nname: prod\n")

	_, err := Load(ws, "")
	require.Error(t, err)
	assert.Equal(t, errs.EnvNotFound, errs.CodeOf(err))
	e := errs.As(err)
	assert.NotEmpty(t, e.Hint)
	assert.ElementsMatch(t, []string{"staging", "prod"}, e.Details["available"])
}

func TestLoad_NotFound_ListsAvailable(t *testing.T) {
	ws := testWorkspace(t)
	writeEnvFile(t, ws, "staging", "version: 1\nname: staging\n")
	writeEnvFile(t, ws, "prod", "version: 1\nname: prod\n")

	_, err := Load(ws, "does-not-exist")
	require.Error(t, err)
	assert.Equal(t, errs.EnvNotFound, errs.CodeOf(err))
	e := errs.As(err)
	assert.ElementsMatch(t, []string{"staging", "prod"}, e.Details["available"])
	assert.Contains(t, e.Hint, "available environments: prod, staging")
}

func TestLoad_NotFound_NoEnvironmentsAtAll(t *testing.T) {
	ws := testWorkspace(t) // fresh temp dir, no environments/ directory at all

	_, err := Load(ws, "does-not-exist")
	require.Error(t, err)
	assert.Equal(t, errs.EnvNotFound, errs.CodeOf(err))
	e := errs.As(err)
	assert.Empty(t, e.Details["available"])
	assert.Contains(t, e.Hint, "no environments exist yet")
}

func TestList(t *testing.T) {
	ws := testWorkspace(t)
	writeEnvFile(t, ws, "staging", "version: 1\nname: staging\n")
	writeEnvFile(t, ws, "local", "version: 1\nname: local\n")

	envs, err := List(ws)
	require.NoError(t, err)
	require.Len(t, envs, 2)
	assert.Equal(t, "local", envs[0].Name)
	assert.Equal(t, "staging", envs[1].Name)
}
