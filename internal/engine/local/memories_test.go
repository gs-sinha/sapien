package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

// qcomMemory is the PLAN §10 worked example: qcomSkill means QCOM-eligible,
// not online.
func qcomMemory() domain.Memory {
	return domain.Memory{
		Type:  domain.MemorySemantic,
		Scope: domain.ScopeWorkspace,
		Subject: domain.Subject{
			Operation: "rider-service.getRider",
			Field:     "response.200.body.qcomSkill",
		},
		Tags: []string{"qcom", "allocation"},
		Text: "qcomSkill indicates whether the rider is eligible for quick-commerce " +
			"(QCOM) orders. It does not indicate the rider is online or available.\n\n" +
			"Implications: QCOM allocation should only select riders with qcomSkill=true.",
	}
}

func TestMemories_Create_WorkspaceScope(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, domain.ScopeWorkspace, created.Scope)
	require.NotEmpty(t, created.FilePath)
	assert.Equal(t, filepath.Join(ws.Dir, domain.MemoriesDir), filepath.Dir(created.FilePath))
	assert.True(t, strings.HasSuffix(created.FilePath, ".md"))
	assert.FileExists(t, created.FilePath)

	got, err := l.Memories().Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.Text, got.Text)
}

func TestMemories_SearchAndRelevant(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)

	results, err := l.Memories().Search(ctx, domain.MemoryQuery{Text: "qcom"})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, created.ID, results[0].Memory.ID)

	relevant, err := l.Memories().Relevant(ctx, []domain.Subject{{Operation: "rider-service.getRider"}}, 10)
	require.NoError(t, err)
	found := false
	for _, m := range relevant {
		if m.Memory.ID == created.ID {
			found = true
		}
	}
	assert.True(t, found, "expected memory %s among Relevant results for rider-service.getRider", created.ID)

	list, err := l.Memories().List(ctx, domain.MemoryQuery{})
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestMemories_UpdateDelete(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)

	toUpdate := *created
	toUpdate.Text = "Updated: " + created.Text
	updated, err := l.Memories().Update(ctx, toUpdate)
	require.NoError(t, err)
	assert.Contains(t, updated.Text, "Updated:")

	require.NoError(t, l.Memories().Delete(ctx, created.ID))
	_, err = l.Memories().Get(ctx, created.ID)
	require.Error(t, err)
	assert.Equal(t, errs.MemoryNotFound, errs.CodeOf(err))
	_, statErr := os.Stat(created.FilePath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestMemories_PromotionTarget_OpenAPIField(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	created, err := l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)

	target, err := l.Memories().PromotionTarget(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, target)

	assert.Equal(t, "openapi", target.Kind)
	assert.Contains(t, target.File, "rider-service")
	assert.Contains(t, target.File, "openapi.yaml")
	assert.Greater(t, target.Line, 0)
	assert.Contains(t, target.Pointer, "qcomSkill")
	assert.Contains(t, target.Current, "QCOM")
	assert.Contains(t, target.Suggested, "description:")
}

func TestMemories_PromotionTarget_OpenAPIOperation(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	mem := domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Operation: "allocation-service.allocate"},
		Text:    "allocate always returns 409 NO_RIDER_AVAILABLE when nobody with qcomSkill is online.",
	}
	created, err := l.Memories().Create(ctx, mem)
	require.NoError(t, err)

	target, err := l.Memories().PromotionTarget(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "openapi", target.Kind)
	assert.Contains(t, target.File, "allocation-service")
	assert.NotEmpty(t, target.Current)
	assert.Equal(t, "/paths/~1v1~1allocations/post", target.Pointer)
}

func TestMemories_PromotionTarget_Doc(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	mem := domain.Memory{
		Type:    domain.MemoryBehavioral,
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Service: "allocation-service"},
		Text:    "Allocation always prefers the first eligible online rider in registration order; it is not load-balanced.",
	}
	created, err := l.Memories().Create(ctx, mem)
	require.NoError(t, err)

	target, err := l.Memories().PromotionTarget(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "doc", target.Kind)
	assert.Contains(t, target.File, "allocation-service")
	assert.Equal(t, created.Text, target.Suggested)
	assert.NotEmpty(t, target.Section)
}

func TestMemories_Reindex(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	_, err = l.Memories().Create(ctx, qcomMemory())
	require.NoError(t, err)
	require.NoError(t, l.Memories().Reindex(ctx))

	list, err := l.Memories().List(ctx, domain.MemoryQuery{})
	require.NoError(t, err)
	assert.Len(t, list, 1)
}
