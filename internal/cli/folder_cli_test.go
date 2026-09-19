package cli_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

// These tests drive PLAN §34f item 6's `mv` commands and `--folder` flags
// against the enginetest fake (setupFakeEngine): `flow mv`/`memory
// mv`/`example mv` (distinct from `flow rescope`/`memory move`/`example
// move`, which move between tiers and keep folder), and `--folder` on
// `list` and (where they exist) `create` commands.

// --- flow mv ---------------------------------------------------------

func TestFlowMv_ForwardsFolder(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	file := writeTemp(t, "mv-demo.flow.yaml", authoringFlow)

	stdout, stderr, code := run(t, "--workspace", dir, "flow", "create", file)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	stdout, stderr, code = run(t, "--workspace", dir, "flow", "mv", "auth-demo", "team/promotions", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "auth-demo", got["id"])
	assert.Equal(t, "team/promotions", got["folder"])

	args := lastCall(fake, "Flows.Move").Args.(map[string]string)
	assert.Equal(t, "auth-demo", args["id"])
	assert.Equal(t, "team/promotions", args["folder"])
}

// --- memory mv --------------------------------------------------------

func TestMemoryMv_ForwardsFolder(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "move me by folder",
	})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "mv", mem.ID, "incidents/2026-09", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, mem.ID, got["id"])
	assert.Equal(t, "incidents/2026-09", got["folder"])

	args := lastCall(fake, "Memories.MoveFolder").Args.(map[string]string)
	assert.Equal(t, mem.ID, args["id"])
	assert.Equal(t, "incidents/2026-09", args["folder"])
}

// "" and "/" both mean root.
func TestMemoryMv_RootAliases(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	mem, err := fake.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "already foldered", Folder: "a",
	})
	require.NoError(t, err)

	_, stderr, code := run(t, "--workspace", dir, "memory", "mv", mem.ID, "/")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	updated, err := fake.Memories().Get(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, "", updated.Folder)
}

// --- example mv -------------------------------------------------------

func TestExampleMv_ForwardsFolder(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	ex, err := fake.Examples().Create(ctx, domain.SavedExample{ID: "mv-example", Operation: "order-service.createOrder"})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "mv", ex.ID, "checkout", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, ex.ID, got["id"])
	assert.Equal(t, "checkout", got["folder"])

	args := lastCall(fake, "Examples.MoveFolder").Args.(map[string]string)
	assert.Equal(t, ex.ID, args["id"])
	assert.Equal(t, "checkout", args["folder"])
}

// --- --folder filters on list commands ---------------------------------

func TestFlowList_FolderFlag(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	file := writeTemp(t, "filter-demo.flow.yaml", authoringFlow)
	_, stderr, code := run(t, "--workspace", dir, "flow", "create", file)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	_, err := fake.Flows().Move(ctx, "auth-demo", "a")
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "flow", "list", "--folder", "a", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0]["folder"])
}

func TestMemoryList_FolderFlag_ShowsColumnOnlyWhenUsed(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	_, err := fake.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "no folder here",
	})
	require.NoError(t, err)

	// Without --folder and no foldered memories, the table has no FOLDER column.
	stdout, stderr, code := run(t, "--workspace", dir, "memory", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stdout, "FOLDER")

	foldered, err := fake.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace, Source: domain.MemorySource{Kind: "user"}, Text: "has a folder", Folder: "a",
	})
	require.NoError(t, err)

	stdout, stderr, code = run(t, "--workspace", dir, "memory", "list", "--folder", "a", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 1)
	assert.Equal(t, foldered.ID, got[0]["id"])

	// Now with a foldered memory present, the human table shows the column.
	stdout, stderr, code = run(t, "--workspace", dir, "memory", "list")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "FOLDER")
}

func TestExampleList_FolderFlag(t *testing.T) {
	dir, fake := setupFakeEngine(t)
	ctx := context.Background()

	_, err := fake.Examples().Create(ctx, domain.SavedExample{ID: "root-ex", Operation: "order-service.createOrder"})
	require.NoError(t, err)
	_, err = fake.Examples().Create(ctx, domain.SavedExample{ID: "foldered-ex", Operation: "order-service.createOrder", Folder: "a"})
	require.NoError(t, err)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "list", "--folder", "a", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "foldered-ex", got[0]["id"])
}

// --- --folder on create commands ---------------------------------------

func TestFlowCreate_FolderFlag(t *testing.T) {
	dir, _ := setupFakeEngine(t)
	file := writeTemp(t, "folder-create.flow.yaml", "version: 1\nid: folder-create\nsteps:\n  - id: a\n    call: order-service.createOrder\n")

	stdout, stderr, code := run(t, "--workspace", dir, "flow", "create", file, "--folder", "team/onboarding", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "team/onboarding/folder-create.flow.yaml", got["path"], "path should reflect the folder for a workspace-tier create")
}

func TestExampleAdd_FolderFlag(t *testing.T) {
	dir, _ := setupFakeEngine(t)

	stdout, stderr, code := run(t, "--workspace", dir, "example", "add", "order-service.createOrder", "--id", "folder-example", "--folder", "checkout", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "checkout", got["folder"])
}

func TestMemoryAdd_FolderFlag(t *testing.T) {
	dir, _ := setupFakeEngine(t)

	stdout, stderr, code := run(t, "--workspace", dir, "memory", "add", "folder note", "--folder", "notes", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "notes", got["folder"])
}
