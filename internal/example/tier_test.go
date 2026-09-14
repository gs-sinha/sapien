package example_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/example"
)

// newTierTestStore is newTestStore with a local tier configured (LocalDir
// set), so PLAN §7b's tiering has somewhere to place a local-tier example
// -- newTestStore itself leaves LocalDir unset, which collapses local and
// workspace into the same directory.
func newTierTestStore(t *testing.T) (*example.Store, example.Locator) {
	t.Helper()
	db := openTestDB(t)
	ws := t.TempDir()
	svcDir := t.TempDir()
	loc := example.Locator{
		WorkspaceDir: ws,
		LocalDir:     filepath.Join(ws, "local"),
		ServiceDirs:  map[string]string{"order-service": svcDir},
	}
	return example.New(db, loc), loc
}

func validExample(id string) domain.SavedExample {
	return domain.SavedExample{
		ID:        id,
		Operation: "order-service.createOrder",
		Body:      map[string]any{"customerId": "c1"},
	}
}

// A newly created workspace-scope example lands in the local tier by
// default (PLAN §7b), same as a new memory or flow.
func TestStore_Create_WorkspaceScope_DefaultsToLocalTier(t *testing.T) {
	ctx := context.Background()
	s, loc := newTierTestStore(t)

	got, err := s.Create(ctx, validExample("local-by-default"))
	require.NoError(t, err)
	assert.Equal(t, domain.TierLocal, got.Tier)
	assert.Equal(t, filepath.Join(loc.LocalDir, "examples", "local-by-default.example.yaml"), got.Path)
	assert.FileExists(t, got.Path)

	reGet, err := s.Get(ctx, got.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.TierLocal, reGet.Tier)
}

// Creating directly into the workspace tier writes under the team's
// examples/, not local/.
func TestStore_Create_WorkspaceScope_ExplicitWorkspaceTier(t *testing.T) {
	ctx := context.Background()
	s, loc := newTierTestStore(t)

	ex := validExample("straight-to-team")
	ex.Tier = domain.TierWorkspace
	got, err := s.Create(ctx, ex)
	require.NoError(t, err)
	assert.Equal(t, domain.TierWorkspace, got.Tier)
	assert.Equal(t, filepath.Join(loc.WorkspaceDir, "examples", "straight-to-team.example.yaml"), got.Path)
}

// A service-scoped example always reports TierService.
func TestStore_Create_ServiceScope_ReportsTierService(t *testing.T) {
	ctx := context.Background()
	s, _ := newTierTestStore(t)

	ex := validExample("svc-scoped")
	ex.Scope = domain.ExampleScopeService
	got, err := s.Create(ctx, ex)
	require.NoError(t, err)
	assert.Equal(t, domain.TierService, got.Tier)
}

// Update with Tier set to the other tier moves the file -- the mechanism
// engine.ExampleAPI.Move rides.
func TestStore_Update_ChangingTierMovesTheFile(t *testing.T) {
	ctx := context.Background()
	s, loc := newTierTestStore(t)

	created, err := s.Create(ctx, validExample("moving-day"))
	require.NoError(t, err)
	require.Equal(t, domain.TierLocal, created.Tier)
	localPath := created.Path
	require.FileExists(t, localPath)

	moved := *created
	moved.Tier = domain.TierWorkspace
	got, err := s.Update(ctx, moved)
	require.NoError(t, err)
	assert.Equal(t, domain.TierWorkspace, got.Tier)
	assert.Equal(t, filepath.Join(loc.WorkspaceDir, "examples", "moving-day.example.yaml"), got.Path)
	assert.FileExists(t, got.Path)

	_, statErr := os.Stat(localPath)
	assert.True(t, os.IsNotExist(statErr), "the local-tier file must be removed after the move")

	// And back.
	back := *got
	back.Tier = domain.TierLocal
	got2, err := s.Update(ctx, back)
	require.NoError(t, err)
	assert.Equal(t, domain.TierLocal, got2.Tier)
	assert.Equal(t, localPath, got2.Path)
	assert.FileExists(t, got2.Path)
}

// A plain Update that does not set Tier keeps the example in whatever tier
// it was already in -- see memory's identical regression test for why this
// matters (PathFor's "unset Tier defaults to local" would otherwise read a
// field-only edit as a request to move a workspace-tier example back to
// local).
func TestStore_Update_WithoutTierKeepsCurrentTier(t *testing.T) {
	ctx := context.Background()
	s, loc := newTierTestStore(t)

	ex := validExample("shipped-example")
	ex.Tier = domain.TierWorkspace
	created, err := s.Create(ctx, ex)
	require.NoError(t, err)
	require.Equal(t, domain.TierWorkspace, created.Tier)

	edited := *created
	edited.Tier = "" // the caller changed nothing about the tier
	edited.Description = "corrected description"
	got, err := s.Update(ctx, edited)
	require.NoError(t, err)
	assert.Equal(t, domain.TierWorkspace, got.Tier, "an unrelated edit must not move the example back to local")
	assert.Equal(t, filepath.Join(loc.WorkspaceDir, "examples", "shipped-example.example.yaml"), got.Path)
	assert.Equal(t, "corrected description", got.Description)
}

// List derives Tier for every row the same way Get does.
func TestStore_List_DerivesTier(t *testing.T) {
	ctx := context.Background()
	s, _ := newTierTestStore(t)

	_, err := s.Create(ctx, validExample("local-one"))
	require.NoError(t, err)
	ex := validExample("workspace-one")
	ex.Tier = domain.TierWorkspace
	_, err = s.Create(ctx, ex)
	require.NoError(t, err)

	got, err := s.List(ctx, domain.ExampleQuery{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	tiers := map[string]bool{}
	for _, e := range got {
		tiers[e.Tier] = true
	}
	assert.True(t, tiers[domain.TierLocal])
	assert.True(t, tiers[domain.TierWorkspace])
}

// Reindex picks up an example file dropped directly into the local tier's
// examples/ directory, same as it already does for the workspace tier --
// Locator.files must be scanning local/examples too.
func TestStore_Reindex_PicksUpLocalTierFile(t *testing.T) {
	ctx := context.Background()
	s, loc := newTierTestStore(t)

	localDir := filepath.Join(loc.LocalDir, "examples")
	require.NoError(t, os.MkdirAll(localDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "dropped-in.example.yaml"), []byte(`version: 1
id: dropped-in
operation: order-service.createOrder
body: { customerId: c1 }
`), 0o644))

	count, err := s.Reindex(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	got, err := s.Get(ctx, "dropped-in")
	require.NoError(t, err)
	assert.Equal(t, domain.TierLocal, got.Tier)
	assert.Equal(t, domain.ExampleScopeWorkspace, got.Scope)
}
