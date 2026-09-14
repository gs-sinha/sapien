package example_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/example"
)

func TestFileName(t *testing.T) {
	assert.Equal(t, "create-qcom-order.example.yaml", example.FileName("create-qcom-order"))
}

func TestLocator_PathFor(t *testing.T) {
	ws := "/tmp/ws"
	svcDir := "/tmp/order-service"
	loc := example.Locator{
		WorkspaceDir: ws,
		ServiceDirs:  map[string]string{"order-service": svcDir},
	}
	locWithLocal := loc
	locWithLocal.LocalDir = filepath.Join(ws, "local")

	tests := []struct {
		name    string
		ex      domain.SavedExample
		want    string
		wantErr errs.Code
	}{
		{
			name: "workspace scope",
			ex:   domain.SavedExample{ID: "ex1", Scope: domain.ExampleScopeWorkspace},
			want: filepath.Join(ws, "examples", "ex1.example.yaml"),
		},
		{
			name: "empty scope defaults to workspace",
			ex:   domain.SavedExample{ID: "ex1"},
			want: filepath.Join(ws, "examples", "ex1.example.yaml"),
		},
		{
			name: "service scope with known dir",
			ex:   domain.SavedExample{ID: "ex2", Scope: domain.ExampleScopeService, Service: "order-service"},
			want: filepath.Join(svcDir, "examples", "ex2.example.yaml"),
		},
		{
			name:    "service scope with unknown service",
			ex:      domain.SavedExample{ID: "ex3", Scope: domain.ExampleScopeService, Service: "rider-service"},
			wantErr: errs.Invalid,
		},
		{
			name:    "unknown scope",
			ex:      domain.SavedExample{ID: "ex4", Scope: domain.ExampleScope("bogus")},
			wantErr: errs.Invalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loc.PathFor(tt.ex)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, errs.CodeOf(err))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	// PLAN §7b: a workspace-scope example's file lives in one of two
	// tiers, decided by Tier (mirrors memory.Locator.DirFor).
	t.Run("workspace tier: unset Tier defaults to local when a local tier exists", func(t *testing.T) {
		got, err := locWithLocal.PathFor(domain.SavedExample{ID: "ex1", Scope: domain.ExampleScopeWorkspace})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(locWithLocal.LocalDir, "examples", "ex1.example.yaml"), got)
	})

	t.Run("workspace tier: explicit TierLocal goes to the local tier", func(t *testing.T) {
		got, err := locWithLocal.PathFor(domain.SavedExample{ID: "ex1", Scope: domain.ExampleScopeWorkspace, Tier: domain.TierLocal})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(locWithLocal.LocalDir, "examples", "ex1.example.yaml"), got)
	})

	t.Run("workspace tier: TierWorkspace goes to the team's examples/", func(t *testing.T) {
		got, err := locWithLocal.PathFor(domain.SavedExample{ID: "ex1", Scope: domain.ExampleScopeWorkspace, Tier: domain.TierWorkspace})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(ws, "examples", "ex1.example.yaml"), got)
	})

	t.Run("workspace tier: without a local tier configured, TierLocal still resolves to workspace", func(t *testing.T) {
		got, err := loc.PathFor(domain.SavedExample{ID: "ex1", Scope: domain.ExampleScopeWorkspace, Tier: domain.TierLocal})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(ws, "examples", "ex1.example.yaml"), got)
	})
}
