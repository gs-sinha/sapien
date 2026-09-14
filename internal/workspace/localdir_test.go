package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

func TestLocalDir_Paths(t *testing.T) {
	ws := &domain.Workspace{Dir: "/ws"}
	assert.Equal(t, filepath.Join("/ws", "local"), LocalDir(ws))
	assert.Equal(t, filepath.Join("/ws", "local", "flows"), LocalFlowsDir(ws))
	assert.Equal(t, filepath.Join("/ws", "local", "memories"), LocalMemoriesDir(ws))
	assert.Equal(t, filepath.Join("/ws", "local", "examples"), LocalExamplesDir(ws))
}

// A fresh workspace has no local/ (Init does not create it); the first
// local write creates the tier and makes it self-ignoring in one step.
func TestEnsureLocalDir_CreatesSelfIgnoringTier(t *testing.T) {
	ws, err := Init(t.TempDir(), "w")
	require.NoError(t, err)
	_, statErr := os.Stat(LocalDir(ws))
	require.True(t, os.IsNotExist(statErr), "Init must not create local/")

	require.NoError(t, EnsureLocalDir(ws))
	assert.DirExists(t, LocalFlowsDir(ws))
	assert.DirExists(t, LocalMemoriesDir(ws))
	assert.DirExists(t, LocalExamplesDir(ws))
	data, err := os.ReadFile(filepath.Join(LocalDir(ws), ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, "*\n", string(data))
}

// Calling it again neither fails nor rewrites an ignore file the developer
// may have edited by hand.
func TestEnsureLocalDir_Idempotent_KeepsExistingGitignore(t *testing.T) {
	ws, err := Init(t.TempDir(), "w")
	require.NoError(t, err)
	require.NoError(t, EnsureLocalDir(ws))

	ignore := filepath.Join(LocalDir(ws), ".gitignore")
	require.NoError(t, os.WriteFile(ignore, []byte("*\n!keep.md\n"), 0o644))

	require.NoError(t, EnsureLocalDir(ws))
	data, err := os.ReadFile(ignore)
	require.NoError(t, err)
	assert.Equal(t, "*\n!keep.md\n", string(data))
}
