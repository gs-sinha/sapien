package memory_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/memory"
)

// newTierTestStore is newTestStore with a local tier configured (LocalDir
// set), so PLAN §7b's tiering actually has somewhere to place a local-tier
// memory -- newTestStore itself deliberately leaves LocalDir unset, which
// collapses local and workspace into the same directory and would hide
// every assertion below.
func newTierTestStore(t *testing.T) (*memory.Store, memory.Locator) {
	t.Helper()
	db := openTestDB(t)
	ws := t.TempDir()
	loc := memory.Locator{
		WorkspaceDir: ws,
		LocalDir:     filepath.Join(ws, "local"),
	}
	return memory.New(db, loc, nil), loc
}

// A newly created workspace-scope memory lands in the local tier by
// default, same as a new flow, and Tier on the returned/read record says so
// (PLAN §7b: "New workspace-scope memories ... land in the LOCAL tier by
// default").
func TestStore_Create_WorkspaceScope_DefaultsToLocalTier(t *testing.T) {
	ctx := context.Background()
	s, loc := newTierTestStore(t)

	got, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "local by default"})
	require.NoError(t, err)
	assert.Equal(t, domain.TierLocal, got.Tier)
	assert.Equal(t, filepath.Join(loc.LocalDir, "memories", memory.FileName(got.ID)), got.FilePath)
	assert.FileExists(t, got.FilePath)

	// Get derives Tier the same way (no tier column to have persisted it).
	reGet, err := s.Get(ctx, got.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.TierLocal, reGet.Tier)
}

// Creating directly into the workspace tier (Tier set explicitly) writes
// under the team's memories/, not local/.
func TestStore_Create_WorkspaceScope_ExplicitWorkspaceTier(t *testing.T) {
	ctx := context.Background()
	s, loc := newTierTestStore(t)

	got, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Tier: domain.TierWorkspace, Text: "straight to the team"})
	require.NoError(t, err)
	assert.Equal(t, domain.TierWorkspace, got.Tier)
	assert.Equal(t, filepath.Join(loc.WorkspaceDir, "memories", memory.FileName(got.ID)), got.FilePath)
}

// Update with Tier set to the other tier moves the file (this is the
// mechanism engine.MemoryAPI.Move rides): the old file is gone, the new one
// exists at the new tier's directory, content and id are unchanged.
func TestStore_Update_ChangingTierMovesTheFile(t *testing.T) {
	ctx := context.Background()
	s, loc := newTierTestStore(t)

	created, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "moving day"})
	require.NoError(t, err)
	require.Equal(t, domain.TierLocal, created.Tier)
	localPath := created.FilePath
	require.FileExists(t, localPath)

	moved := *created
	moved.Tier = domain.TierWorkspace
	got, err := s.Update(ctx, moved)
	require.NoError(t, err)
	assert.Equal(t, domain.TierWorkspace, got.Tier)
	assert.Equal(t, filepath.Join(loc.WorkspaceDir, "memories", memory.FileName(created.ID)), got.FilePath)
	assert.FileExists(t, got.FilePath)

	_, statErr := os.Stat(localPath)
	assert.True(t, os.IsNotExist(statErr), "the local-tier file must be removed after the move")

	data, err := os.ReadFile(got.FilePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "moving day")

	// And moving back to local works the same way, in the other direction.
	back := *got
	back.Tier = domain.TierLocal
	got2, err := s.Update(ctx, back)
	require.NoError(t, err)
	assert.Equal(t, domain.TierLocal, got2.Tier)
	assert.Equal(t, localPath, got2.FilePath)
	assert.FileExists(t, got2.FilePath)
}

// A plain Update that does not set Tier keeps the memory in whatever tier
// it was already in -- the regression PLAN §7b calls out by name: without
// this, a caller doing nothing but editing text would read as "no tier
// requested", which DirFor's default treats as "put it in local", silently
// moving a workspace-tier memory back to local on every unrelated edit.
func TestStore_Update_WithoutTierKeepsCurrentTier(t *testing.T) {
	ctx := context.Background()
	s, loc := newTierTestStore(t)

	created, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Tier: domain.TierWorkspace, Text: "shipped knowledge"})
	require.NoError(t, err)
	require.Equal(t, domain.TierWorkspace, created.Tier)

	edited := *created
	edited.Tier = "" // the caller changed nothing about the tier
	edited.Text = "shipped knowledge, corrected"
	got, err := s.Update(ctx, edited)
	require.NoError(t, err)
	assert.Equal(t, domain.TierWorkspace, got.Tier, "an unrelated edit must not move the memory back to local")
	assert.Equal(t, filepath.Join(loc.WorkspaceDir, "memories", memory.FileName(created.ID)), got.FilePath)
	assert.Equal(t, "shipped knowledge, corrected", got.Text)
}

// A service-scoped memory always reports TierService, and a flow-scoped
// memory's Tier follows whichever tier its flow's owner resolves to via
// FlowOwner -- Tier describes where the file physically is, regardless of
// Scope (PLAN §7b, and the domain.Memory.Tier doc comment).
func TestStore_TierDerivation_ServiceAndFlowScope(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	ws := t.TempDir()
	svcDir := t.TempDir()
	loc := memory.Locator{
		WorkspaceDir: ws,
		LocalDir:     filepath.Join(ws, "local"),
		ServiceDirs:  map[string]string{"rider-service": svcDir},
		FlowOwner: func(flowID string) (string, string) {
			if flowID == "local-flow" {
				return domain.FlowOwnerLocal, ""
			}
			return domain.FlowOwnerWorkspace, ""
		},
	}
	s := memory.New(db, loc, nil)

	svcMem, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeService,
		Subject: domain.Subject{Service: "rider-service"},
		Text:    "service fact",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.TierService, svcMem.Tier)

	localFlowMem, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeFlow,
		Subject: domain.Subject{Flow: "local-flow"},
		Text:    "local flow fact",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.TierLocal, localFlowMem.Tier)

	wsFlowMem, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeFlow,
		Subject: domain.Subject{Flow: "team-flow"},
		Text:    "team flow fact",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.TierWorkspace, wsFlowMem.Tier)
}

// Personal-scope memories have no file and no tier.
func TestStore_TierDerivation_PersonalScopeIsEmpty(t *testing.T) {
	ctx := context.Background()
	s, _ := newTierTestStore(t)

	got, err := s.Create(ctx, domain.Memory{Scope: domain.ScopePersonal, Text: "just for me"})
	require.NoError(t, err)
	assert.Empty(t, got.Tier)
	assert.Empty(t, got.FilePath)
}

// List derives Tier for every row the same way Get does.
func TestStore_List_DerivesTier(t *testing.T) {
	ctx := context.Background()
	s, _ := newTierTestStore(t)

	_, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "local one"})
	require.NoError(t, err)
	_, err = s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Tier: domain.TierWorkspace, Text: "workspace one"})
	require.NoError(t, err)

	got, err := s.List(ctx, domain.MemoryQuery{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	tiers := map[string]bool{}
	for _, m := range got {
		tiers[m.Tier] = true
	}
	assert.True(t, tiers[domain.TierLocal])
	assert.True(t, tiers[domain.TierWorkspace])
}
