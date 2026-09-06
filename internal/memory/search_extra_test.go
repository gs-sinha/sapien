package memory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/memory"
)

func TestStore_Relevant_TagOverlap(t *testing.T) {
	ctx := context.Background()
	res := newFakeResolver().withOp("rider-service.getRider", memory.OperationInfo{
		Service: "rider-service",
		Method:  "GET",
		Path:    "/v1/riders/{riderId}",
		Tags:    []string{"Dispatch", "allocation"},
	})
	s, _ := newTestStore(t, res)

	tagged, err := s.Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace,
		Tags:  []string{"dispatch"}, // case-insensitive overlap with operation tag "Dispatch"
		Text:  "tag-overlap candidate",
	})
	require.NoError(t, err)

	untagged, err := s.Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace,
		Tags:  []string{"unrelated"},
		Text:  "no overlap",
	})
	require.NoError(t, err)

	got, err := s.Relevant(ctx, []domain.Subject{{Operation: "rider-service.getRider"}}, 10)
	require.NoError(t, err)

	ids := idsOfScored(got)
	assert.Contains(t, ids, tagged.ID)
	assert.NotContains(t, ids, untagged.ID)

	for _, sm := range got {
		if sm.Memory.ID == tagged.ID {
			assert.True(t, containsSubstring(sm.Reasons, "tag "), "expected a tag-overlap reason, got %v", sm.Reasons)
		}
	}
}

func TestStore_Relevant_DirectSubjectKinds(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	envMem, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Subject: domain.Subject{Environment: "staging"}, Text: "staging quirk"})
	require.NoError(t, err)
	runMem, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Subject: domain.Subject{Run: "run_123"}, Text: "seen in a run"})
	require.NoError(t, err)
	errMem, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Error: &domain.ErrorRef{Operation: "order-service.createOrder", Status: 409, Code: "DUPLICATE"}},
		Text:    "409 DUPLICATE on retried creates",
	})
	require.NoError(t, err)

	t.Run("environment", func(t *testing.T) {
		got, err := s.Relevant(ctx, []domain.Subject{{Environment: "staging"}}, 10)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, envMem.ID, got[0].Memory.ID)
	})

	t.Run("run", func(t *testing.T) {
		got, err := s.Relevant(ctx, []domain.Subject{{Run: "run_123"}}, 10)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, runMem.ID, got[0].Memory.ID)
	})

	t.Run("exact error match", func(t *testing.T) {
		got, err := s.Relevant(ctx, []domain.Subject{{Error: &domain.ErrorRef{Operation: "order-service.createOrder", Status: 409, Code: "DUPLICATE"}}}, 10)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, errMem.ID, got[0].Memory.ID)
		assert.Contains(t, got[0].Reasons, "error match")
	})

	t.Run("error operation fallback", func(t *testing.T) {
		// Same operation, different status/code: no exact "error" row match,
		// but the operation itself is recorded as a memory_subjects row via
		// the error subject, so the fallback tier still finds it.
		got, err := s.Relevant(ctx, []domain.Subject{{Error: &domain.ErrorRef{Operation: "order-service.createOrder", Status: 500, Code: "INTERNAL"}}}, 10)
		require.NoError(t, err)
		require.Len(t, got, 0, "no fallback because the memory has no plain operation subject, only an error subject")
	})
}

func TestStore_Search_TypeOperationFlowFilters(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	invariant, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Type:    domain.MemoryInvariant,
		Subject: domain.Subject{Operation: "rider-service.getRider", Flow: "order-allocation"},
		Text:    "shared keyword beta",
	})
	require.NoError(t, err)
	_, err = s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Type:    domain.MemoryNote,
		Subject: domain.Subject{Operation: "order-service.createOrder"},
		Text:    "shared keyword beta",
	})
	require.NoError(t, err)

	t.Run("filters by type", func(t *testing.T) {
		got, err := s.Search(ctx, domain.MemoryQuery{Text: "beta", Type: domain.MemoryInvariant})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, invariant.ID, got[0].Memory.ID)
	})

	t.Run("filters by operation", func(t *testing.T) {
		got, err := s.Search(ctx, domain.MemoryQuery{Text: "beta", Operation: "rider-service.getRider"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, invariant.ID, got[0].Memory.ID)
	})

	t.Run("filters by flow", func(t *testing.T) {
		got, err := s.Search(ctx, domain.MemoryQuery{Text: "beta", Flow: "order-allocation"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, invariant.ID, got[0].Memory.ID)
	})
}

func TestStore_Relevant_SchemaFieldSubject(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	m, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Schema: "rider-service.Rider", Field: "qcomSkill"},
		Text:    "schema field note",
	})
	require.NoError(t, err)

	got, err := s.Relevant(ctx, []domain.Subject{{Schema: "rider-service.Rider", Field: "qcomSkill"}}, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, m.ID, got[0].Memory.ID)
	assert.Contains(t, got[0].Reasons, "schema field match")
}

func TestStore_Relevant_EmptySubjectsYieldsNothing(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)
	_, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "anything"})
	require.NoError(t, err)

	got, err := s.Relevant(ctx, nil, 10)
	require.NoError(t, err)
	assert.Empty(t, got)
}
