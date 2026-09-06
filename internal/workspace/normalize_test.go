package workspace_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/workspace"
)

func TestNormalizeLocalPath(t *testing.T) {
	wsDir := filepath.Join(t.TempDir(), "ws")
	ws := &domain.Workspace{Dir: wsDir}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"inside workspace becomes relative", filepath.Join(wsDir, "services", "rider"), filepath.Join("services", "rider")},
		{"inside workspace with dot segments", filepath.Join(wsDir, "a", "..", "rider"), "rider"},
		{"the workspace itself", wsDir, "."},
		{"outside workspace stays absolute", filepath.Join(filepath.Dir(wsDir), "elsewhere", "svc"), filepath.Join(filepath.Dir(wsDir), "elsewhere", "svc")},
		{"sibling with the workspace name as prefix stays absolute", wsDir + "2", wsDir + "2"},
		{"already relative is untouched", "services/rider", "services/rider"},
		{"home-relative is untouched", "~/code/rider", "~/code/rider"},
		{"empty is untouched", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, workspace.NormalizeLocalPath(ws, tc.in))
		})
	}
}

func TestNormalizeLocalPath_NilWorkspace(t *testing.T) {
	assert.Equal(t, "/x/y", workspace.NormalizeLocalPath(nil, "/x/y"))
	assert.Equal(t, "/x/y", workspace.NormalizeLocalPath(&domain.Workspace{}, "/x/y"))
}
