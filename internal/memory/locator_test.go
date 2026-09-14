package memory_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/memory"
)

func TestLocator_DirFor(t *testing.T) {
	loc := memory.Locator{
		WorkspaceDir: "/ws",
		ServiceDirs: map[string]string{
			"rider-service": "/repos/rider-service/api",
		},
	}

	t.Run("personal is DB only", func(t *testing.T) {
		dir, err := loc.DirFor(domain.Memory{Scope: domain.ScopePersonal})
		require.NoError(t, err)
		assert.Equal(t, "", dir)
	})

	t.Run("workspace", func(t *testing.T) {
		dir, err := loc.DirFor(domain.Memory{Scope: domain.ScopeWorkspace})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/ws", "memories"), dir)
	})

	t.Run("service via subject.service", func(t *testing.T) {
		dir, err := loc.DirFor(domain.Memory{Scope: domain.ScopeService, Subject: domain.Subject{Service: "rider-service"}})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/repos/rider-service/api", "memories"), dir)
	})

	t.Run("service inferred from operation prefix", func(t *testing.T) {
		dir, err := loc.DirFor(domain.Memory{Scope: domain.ScopeService, Subject: domain.Subject{Operation: "rider-service.getRider"}})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/repos/rider-service/api", "memories"), dir)
	})

	t.Run("service scope with no service info errors", func(t *testing.T) {
		_, err := loc.DirFor(domain.Memory{Scope: domain.ScopeService})
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})

	t.Run("service scope with unknown service errors", func(t *testing.T) {
		_, err := loc.DirFor(domain.Memory{Scope: domain.ScopeService, Subject: domain.Subject{Service: "unknown-service"}})
		require.Error(t, err)
		assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
	})

	t.Run("flow scope with no subject.flow errors", func(t *testing.T) {
		_, err := loc.DirFor(domain.Memory{Scope: domain.ScopeFlow})
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})

	t.Run("flow scope with nil FlowOwner defaults to workspace", func(t *testing.T) {
		dir, err := loc.DirFor(domain.Memory{Scope: domain.ScopeFlow, Subject: domain.Subject{Flow: "order-allocation"}})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/ws", "memories"), dir)
	})

	t.Run("flow scope owned by a service via FlowOwner", func(t *testing.T) {
		locWithOwner := loc
		locWithOwner.FlowOwner = func(flowID string) (string, string) {
			if flowID == "rider-service-flow" {
				return "service", "rider-service"
			}
			return "workspace", ""
		}
		dir, err := locWithOwner.DirFor(domain.Memory{Scope: domain.ScopeFlow, Subject: domain.Subject{Flow: "rider-service-flow"}})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/repos/rider-service/api", "memories"), dir)
	})

	t.Run("flow scope owned by workspace via FlowOwner", func(t *testing.T) {
		locWithOwner := loc
		locWithOwner.FlowOwner = func(flowID string) (string, string) { return "workspace", "" }
		dir, err := locWithOwner.DirFor(domain.Memory{Scope: domain.ScopeFlow, Subject: domain.Subject{Flow: "order-allocation"}})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/ws", "memories"), dir)
	})

	// A local-tier flow's memories stay inside the local tier, so nothing
	// about the flow reaches the team's memories/ until it is promoted.
	t.Run("flow scope owned by the local tier lands under local/memories", func(t *testing.T) {
		locWithOwner := loc
		locWithOwner.LocalDir = filepath.Join("/ws", "local")
		locWithOwner.FlowOwner = func(flowID string) (string, string) { return domain.FlowOwnerLocal, "" }
		dir, err := locWithOwner.DirFor(domain.Memory{Scope: domain.ScopeFlow, Subject: domain.Subject{Flow: "scratch"}})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/ws", "local", "memories"), dir)
	})

	// A Locator built without a local tier (LocalDir empty) still has
	// somewhere to put such a memory: the workspace's memories/.
	t.Run("flow scope owned by the local tier without LocalDir falls back to workspace", func(t *testing.T) {
		locWithOwner := loc
		locWithOwner.FlowOwner = func(flowID string) (string, string) { return domain.FlowOwnerLocal, "" }
		dir, err := locWithOwner.DirFor(domain.Memory{Scope: domain.ScopeFlow, Subject: domain.Subject{Flow: "scratch"}})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/ws", "memories"), dir)
	})

	t.Run("unknown scope errors", func(t *testing.T) {
		_, err := loc.DirFor(domain.Memory{Scope: domain.MemoryScope("bogus")})
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})
}

func TestLocator_Files(t *testing.T) {
	ws := t.TempDir()
	svcDir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(ws, "memories"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "memories", "b.md"), []byte("b"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "memories", "a.md"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "memories", "not-markdown.txt"), []byte("x"), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(svcDir, "memories"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "memories", "c.md"), []byte("c"), 0o644))

	loc := memory.Locator{
		WorkspaceDir: ws,
		ServiceDirs:  map[string]string{"rider-service": svcDir},
	}

	files, err := loc.Files()
	require.NoError(t, err)
	require.Len(t, files, 3)
	assert.ElementsMatch(t, []string{
		filepath.Join(svcDir, "memories", "c.md"),
		filepath.Join(ws, "memories", "a.md"),
		filepath.Join(ws, "memories", "b.md"),
	}, files)
	assert.True(t, sort.StringsAreSorted(files), "Files() must return a sorted list, got %v", files)
}

// Reindex reads Files(), so a memory written under local/memories is only
// ever indexed if Files() scans the local tier too.
func TestLocator_Files_IncludesLocalTier(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "memories"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "memories", "team.md"), []byte("t"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "local", "memories"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "local", "memories", "mine.md"), []byte("m"), 0o644))

	loc := memory.Locator{WorkspaceDir: ws, LocalDir: filepath.Join(ws, "local")}
	files, err := loc.Files()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		filepath.Join(ws, "memories", "team.md"),
		filepath.Join(ws, "local", "memories", "mine.md"),
	}, files)
	assert.True(t, sort.StringsAreSorted(files), "Files() must return a sorted list, got %v", files)
}

func TestLocator_Files_MissingDirsAreSkipped(t *testing.T) {
	loc := memory.Locator{
		WorkspaceDir: filepath.Join(t.TempDir(), "does-not-exist"),
		LocalDir:     filepath.Join(t.TempDir(), "no-local-tier"),
		ServiceDirs:  map[string]string{"svc": filepath.Join(t.TempDir(), "also-missing")},
	}
	files, err := loc.Files()
	require.NoError(t, err)
	assert.Empty(t, files)
}
