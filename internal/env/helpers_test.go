package env

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/growsimplee/sapien/internal/domain"
)

// testWorkspace returns a *domain.Workspace rooted at a fresh temp
// directory, suitable for tests that exercise Load/List/DefaultChain
// without any real workspace on disk.
func testWorkspace(t *testing.T) *domain.Workspace {
	t.Helper()
	dir := t.TempDir()
	return &domain.Workspace{
		Version: 1,
		Name:    "testws",
		Dir:     dir,
		File:    filepath.Join(dir, domain.WorkspaceFileName),
	}
}

// writeEnvFile writes a minimal environments/<name>.yaml under ws.Dir.
func writeEnvFile(t *testing.T, ws *domain.Workspace, name, contents string) {
	t.Helper()
	dir := filepath.Join(ws.Dir, domain.EnvironmentsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
