package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/memory"
)

func idOf(sm domain.ScoredMemory) string { return sm.Memory.ID }

func idsOfScored(ms []domain.ScoredMemory) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = idOf(m)
	}
	return out
}

func TestStore_Search_Lexical(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	qcom, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Operation: "rider-service.getRider", Field: "response.200.body.qcomSkill"},
		Text:    "qcomSkill indicates whether the rider is eligible for quick-commerce orders.",
	})
	require.NoError(t, err)

	_, err = s.Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace,
		Text:  "Completely unrelated note about retry backoff timing.",
	})
	require.NoError(t, err)

	got, err := s.Search(ctx, domain.MemoryQuery{Text: "qcom"})
	require.NoError(t, err)
	require.NotEmpty(t, got)
	assert.Equal(t, qcom.ID, got[0].Memory.ID)
	assert.Contains(t, got[0].Reasons, "lexical")
}

func TestStore_Search_StructuralBoostOutranksLexical(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	// The "true" match: attached exactly to the operation+field subject.Search
	// will be called with.
	exact, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Operation: "rider-service.getRider", Field: "response.200.body.qcomSkill"},
		Text:    "qcom eligibility rules for riders",
	})
	require.NoError(t, err)

	// A lexically similar memory on a different, unrelated service/operation:
	// same query terms hit it via FTS, but it has no structural relationship
	// to the queried operation.
	_, err = s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Operation: "order-service.createOrder"},
		Text:    "qcom eligibility rules for riders mentioned here too",
	})
	require.NoError(t, err)

	got, err := s.Search(ctx, domain.MemoryQuery{
		Text:     "qcom eligibility rules",
		Subjects: []domain.Subject{{Operation: "rider-service.getRider"}},
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, exact.ID, got[0].Memory.ID, "the structurally-matched memory must outrank a lexical-only match on another operation")
	assert.Greater(t, got[0].Score, got[1].Score)
	assert.Contains(t, got[0].Reasons, "operation match")
}

func TestStore_Search_Filters(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	riderNote, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Service: "rider-service"},
		Text:    "shared keyword alpha",
	})
	require.NoError(t, err)
	_, err = s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Service: "order-service"},
		Text:    "shared keyword alpha",
	})
	require.NoError(t, err)

	got, err := s.Search(ctx, domain.MemoryQuery{Text: "alpha", Service: "rider-service"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, riderNote.ID, got[0].Memory.ID)
}

func TestStore_Search_ExcludesInactive(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	m, err := s.Create(ctx, domain.Memory{Scope: domain.ScopeWorkspace, Text: "unique needle term"})
	require.NoError(t, err)

	got, err := s.Search(ctx, domain.MemoryQuery{Text: "needle"})
	require.NoError(t, err)
	require.Len(t, got, 1)

	deprecated := *m
	deprecated.Status = domain.MemoryDeprecated
	_, err = s.Update(ctx, deprecated)
	require.NoError(t, err)

	got, err = s.Search(ctx, domain.MemoryQuery{Text: "needle"})
	require.NoError(t, err)
	assert.Empty(t, got, "deprecated memories must be excluded from Search by default")
}

func TestStore_Relevant_ExpandsViaResolver(t *testing.T) {
	ctx := context.Background()
	res := newFakeResolver().
		withOp("rider-service.getRider", memory.OperationInfo{
			Service: "rider-service",
			Method:  "GET",
			Path:    "/v1/riders/{riderId}",
			Hash:    "h1",
			Schemas: []string{"rider-service.Rider"},
			Tags:    []string{"dispatch"},
		}).
		withFlow("rider-service.getRider", "order-allocation")
	s, _ := newTestStore(t, res)

	schemaMem, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Schema: "rider-service.Rider"},
		Text:    "Rider schema notes",
	})
	require.NoError(t, err)

	flowMem, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Flow: "order-allocation"},
		Text:    "order-allocation flow notes",
	})
	require.NoError(t, err)

	serviceMem, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Service: "rider-service"},
		Text:    "rider-service general notes",
	})
	require.NoError(t, err)

	unrelated, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Service: "order-service"},
		Text:    "completely unrelated",
	})
	require.NoError(t, err)

	got, err := s.Relevant(ctx, []domain.Subject{{Operation: "rider-service.getRider"}}, 10)
	require.NoError(t, err)

	ids := idsOfScored(got)
	assert.Contains(t, ids, schemaMem.ID)
	assert.Contains(t, ids, flowMem.ID)
	assert.Contains(t, ids, serviceMem.ID)
	assert.NotContains(t, ids, unrelated.ID)

	byID := map[string]domain.ScoredMemory{}
	for _, sm := range got {
		byID[sm.Memory.ID] = sm
	}
	schemaHit := byID[schemaMem.ID]
	flowHit := byID[flowMem.ID]
	serviceHit := byID[serviceMem.ID]

	assert.True(t, containsSubstring(schemaHit.Reasons, "schema"), "expected a schema reason, got %v", schemaHit.Reasons)
	assert.True(t, containsSubstring(flowHit.Reasons, "flow"), "expected a flow reason, got %v", flowHit.Reasons)
	assert.True(t, containsSubstring(serviceHit.Reasons, "service"), "expected a service reason, got %v", serviceHit.Reasons)

	// Structural tier ordering: schema (0.7) > flow (0.5) > service (0.4).
	assert.Greater(t, schemaHit.Score, flowHit.Score)
	assert.Greater(t, flowHit.Score, serviceHit.Score)
}

func TestStore_Relevant_OperationAndFieldTiers(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	exact, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Operation: "rider-service.getRider", Field: "response.200.body.qcomSkill"},
		Text:    "exact field match",
	})
	require.NoError(t, err)

	opOnly, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Operation: "rider-service.getRider"},
		Text:    "operation-only match",
	})
	require.NoError(t, err)

	got, err := s.Relevant(ctx, []domain.Subject{
		{Operation: "rider-service.getRider", Field: "response.200.body.qcomSkill"},
	}, 10)
	require.NoError(t, err)
	require.Len(t, got, 2)

	byID := map[string]domain.ScoredMemory{}
	for _, sm := range got {
		byID[sm.Memory.ID] = sm
	}
	assert.Greater(t, byID[exact.ID].Score, byID[opOnly.ID].Score)
	assert.Contains(t, byID[exact.ID].Reasons, "operation+field match")
	assert.Contains(t, byID[opOnly.ID].Reasons, "operation match")
	assert.Equal(t, exact.ID, got[0].Memory.ID)
}

func TestStore_Relevant_ExcludesInactive(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	m, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Operation: "rider-service.getRider"},
		Text:    "will be superseded",
	})
	require.NoError(t, err)

	got, err := s.Relevant(ctx, []domain.Subject{{Operation: "rider-service.getRider"}}, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)

	superseded := *m
	superseded.Status = domain.MemorySuperseded
	_, err = s.Update(ctx, superseded)
	require.NoError(t, err)

	got, err = s.Relevant(ctx, []domain.Subject{{Operation: "rider-service.getRider"}}, 10)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestStore_Search_ProvenanceOrdering(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	agentMem, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Concept: "shared-concept-xyz"},
		Source:  domain.MemorySource{Kind: "agent", Client: "claude-code"},
		Text:    "agent authored note about the shared concept",
	})
	require.NoError(t, err)

	userMem, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Concept: "shared-concept-xyz"},
		Source:  domain.MemorySource{Kind: "user"},
		Text:    "user authored note about the shared concept",
	})
	require.NoError(t, err)

	got, err := s.Relevant(ctx, []domain.Subject{{Concept: "shared-concept-xyz"}}, 10)
	require.NoError(t, err)
	require.Len(t, got, 2)

	byID := map[string]domain.ScoredMemory{}
	for _, sm := range got {
		byID[sm.Memory.ID] = sm
	}
	// Same structural tier (concept match) for both, so provenance must break
	// the tie: user (1.0) outranks agent (0.7).
	assert.Greater(t, byID[userMem.ID].Score, byID[agentMem.ID].Score)
	assert.Equal(t, userMem.ID, got[0].Memory.ID)
}

func TestStore_Relevant_RecencyTiebreak(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, nil)

	older, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Concept: "recency-concept"},
		Text:    "older note",
	})
	require.NoError(t, err)

	// Force older's Updated further into the past directly via Update (which
	// always bumps Updated to time.Now()); instead, backdate by writing a
	// file with an old timestamp and reindexing it, which is the supported
	// way to change Updated to an arbitrary value.
	backdated := *older
	backdated.Updated = time.Now().UTC().Add(-30 * 24 * time.Hour)
	require.NoError(t, memory.WriteFile(backdated))
	require.NoError(t, s.IndexOne(ctx, backdated.FilePath))

	newer, err := s.Create(ctx, domain.Memory{
		Scope:   domain.ScopeWorkspace,
		Subject: domain.Subject{Concept: "recency-concept"},
		Text:    "newer note",
	})
	require.NoError(t, err)

	got, err := s.Relevant(ctx, []domain.Subject{{Concept: "recency-concept"}}, 10)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, newer.ID, got[0].Memory.ID, "more recently updated memory should rank first when otherwise tied")
}

func containsSubstring(list []string, substr string) bool {
	for _, s := range list {
		if len(s) >= len(substr) {
			for i := 0; i+len(substr) <= len(s); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}
