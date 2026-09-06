package example_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/example"
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
}
