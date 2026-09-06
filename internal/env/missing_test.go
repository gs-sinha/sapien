package env

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/growsimplee/sapien/internal/domain"
)

func TestMissingForService(t *testing.T) {
	ws := testWorkspace(t)
	writeEnvFile(t, ws, "local", "version: 1\nname: local\n")
	writeEnvFile(t, ws, "prod", "version: 1\nname: prod\n")

	svc := domain.Service{
		Name: "order-service",
		Environments: map[string]domain.EnvHint{
			"local":   {BaseURL: "http://localhost:4010"},
			"stage":   {BaseURL: "http://stage.internal"},
			"preprod": {BaseURL: "http://preprod.internal"},
		},
	}

	got := MissingForService(ws, svc)
	assert.Equal(t, []string{"preprod", "stage"}, got, "local exists as a file, prod is unrelated to this service, so only stage and preprod are missing")
}

func TestMissingForService_NoHints(t *testing.T) {
	ws := testWorkspace(t)
	svc := domain.Service{Name: "order-service"}
	assert.Empty(t, MissingForService(ws, svc))
}

func TestMissingForService_NoEnvironmentsDirectory(t *testing.T) {
	ws := testWorkspace(t) // fresh temp dir, no environments/ at all
	svc := domain.Service{
		Name:         "order-service",
		Environments: map[string]domain.EnvHint{"local": {BaseURL: "http://localhost:4010"}},
	}
	assert.Equal(t, []string{"local"}, MissingForService(ws, svc))
}

func TestMissingForService_EverythingPresent(t *testing.T) {
	ws := testWorkspace(t)
	writeEnvFile(t, ws, "local", "version: 1\nname: local\n")

	svc := domain.Service{
		Name:         "order-service",
		Environments: map[string]domain.EnvHint{"local": {BaseURL: "http://localhost:4010"}},
	}
	assert.Empty(t, MissingForService(ws, svc))
}
