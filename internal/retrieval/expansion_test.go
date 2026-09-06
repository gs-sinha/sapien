package retrieval_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/retrieval"
)

// intentQCOMAllocationTest is the literal PLAN.md §14/Sapien.md §48 success-
// scenario intent. Verified directly against the live FTS/BM25 ranking (see
// TestZZZDebugBuild-style probing used while designing this test): with no
// explicit operations, Search.Operations(ctx, intentQCOMAllocationTest, ...)
// selects order-service.createOrder, allocation-service.getAllocation,
// allocation-service.releaseAllocation, allocation-service.allocate,
// allocation-service.listAllocations,
// allocation-service.get_v1_allocations_stats, order-service.listOrders, and
// order-service.getOrder - never rider-service.getRider, which is exactly
// the documented gap (doc.go) memory-aware expansion closes.
const intentQCOMAllocationTest = "Create a QCOM allocation test"

// TestBuild_MemoryExpansion_AddsSubjectOperation is the PLAN.md §25/Sapien.md
// §23,§31,§48 success scenario end to end: an intent-driven request (no
// explicit operations) for "Create a QCOM allocation test", with the
// qcomSkill invariant memory (saved on rider-service.getRider's response
// field) present, must surface BOTH allocation-service.allocate (found by
// search) AND rider-service.getRider (missed by search, added because the
// retrieved invariant memory's own Subject.Operation names it) - plus the
// memory itself, still in the bundle.
func TestBuild_MemoryExpansion_AddsSubjectOperation(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	invariant, _, _ := createQCOMMemories(t, env)

	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)
	bundle, err := b.Build(ctx, domain.ContextRequest{Intent: intentQCOMAllocationTest})
	require.NoError(t, err)

	require.NotNil(t, findOp(bundle.Operations, "allocation-service.allocate"),
		"expected search to still select allocate, got %+v", bundle.Operations)
	got := findOp(bundle.Operations, "rider-service.getRider")
	require.NotNil(t, got, "expected memory-aware expansion to add getRider, got %+v", bundle.Operations)
	require.Equal(t, domain.TierContract, got.Tier)
	require.NotZero(t, got.Score)

	require.NotNil(t, findMemory(bundle.Memories, invariant.ID),
		"expected the qcomSkill invariant memory to remain in the bundle")
}

// TestBuild_MemoryExpansion_RequiresMemory proves the expansion is actually
// memory-driven (not, say, some latent search change): the identical
// intent-driven request without the qcomSkill memory in the store leaves
// rider-service.getRider absent, while allocate (a genuine search hit) is
// still present.
func TestBuild_MemoryExpansion_RequiresMemory(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)
	bundle, err := b.Build(ctx, domain.ContextRequest{Intent: intentQCOMAllocationTest})
	require.NoError(t, err)

	require.NotNil(t, findOp(bundle.Operations, "allocation-service.allocate"))
	require.Nil(t, findOp(bundle.Operations, "rider-service.getRider"),
		"getRider must not appear without the memory that names it, got %+v", bundle.Operations)
}

// TestBuild_MemoryExpansion_ExplicitOperationsNotExpanded checks that an
// explicit req.Operations list is respected as given: even with the
// qcomSkill memory present (and therefore retrieved into bundle.Memories),
// naming only allocate explicitly must not pull getRider in behind the
// caller's back.
func TestBuild_MemoryExpansion_ExplicitOperationsNotExpanded(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	createQCOMMemories(t, env)

	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)
	bundle, err := b.Build(ctx, domain.ContextRequest{
		Intent:     intentQCOMAllocationTest,
		Operations: []string{"allocation-service.allocate"},
	})
	require.NoError(t, err)

	require.Len(t, bundle.Operations, 1)
	require.Equal(t, "allocation-service.allocate", bundle.Operations[0].ID)
	require.Nil(t, findOp(bundle.Operations, "rider-service.getRider"),
		"explicit operations must never be expanded, got %+v", bundle.Operations)
}

// TestBuild_MemoryExpansion_CapAtThree seeds 5 memories, each about a
// distinct operation unrelated to the other four, all discoverable via the
// same "qcom" concept overlap that the qcomSkill scenario itself relies on,
// and checks that memory-aware expansion adds at most 3 of them.
//
// The initial operation list comes from req.Flow ("smoke": allocate +
// getAllocation), not search, with an empty intent - this keeps the initial
// selection fixed and deterministic (an empty query never matches anything
// lexically) so the test exercises only the expansion cap, not BM25
// ranking.
func TestBuild_MemoryExpansion_CapAtThree(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	targets := []string{
		"allocation-service.releaseAllocation",
		"allocation-service.listAllocations",
		"allocation-service.get_v1_allocations_stats",
		"rider-service.getRider",
		"rider-service.searchRiders",
	}
	for _, opID := range targets {
		createMemory(t, env, domain.Memory{
			Type:  domain.MemoryNote,
			Scope: domain.ScopeWorkspace,
			Subject: domain.Subject{
				Operation: opID,
				Concept:   "qcom",
			},
			Text: "unrelated note about " + opID,
		})
	}

	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)
	bundle, err := b.Build(ctx, domain.ContextRequest{Flow: "smoke"})
	require.NoError(t, err)

	// The flow itself only ever selects allocate + getAllocation.
	require.NotNil(t, findOp(bundle.Operations, "allocation-service.allocate"))
	require.NotNil(t, findOp(bundle.Operations, "allocation-service.getAllocation"))

	added := 0
	for _, opID := range targets {
		if findOp(bundle.Operations, opID) != nil {
			added++
		}
	}
	require.LessOrEqual(t, added, 3, "expansion must add at most 3 operations, got %+v", bundle.Operations)
	// All 5 candidate memories are equally eligible (same structural tier,
	// same missing operation), so the cap is only meaningful if it actually
	// bites here: exactly 3 must be added, not fewer.
	require.Equal(t, 3, added, "expected the cap to add exactly 3 of the 5 eligible operations, got %+v", bundle.Operations)
	require.Len(t, bundle.Operations, 5)
}

// TestBuild_MemoryExpansion_SecondRoundPicksUpMemoryOnExpandedOperation
// checks the bounded "one extra round" of memory retrieval: a testing
// memory attached only to rider-service.getRider, sharing no concept/tag/
// lexical overlap with the original (non-getRider) operation selection or
// with the intent, is invisible to the first memory round entirely - it can
// only be found once getRider itself is a selected operation and
// contributes its own {Operation: "rider-service.getRider"} subject. Because
// the qcomSkill invariant memory pulls getRider in, this memory must appear
// in the final bundle.
func TestBuild_MemoryExpansion_SecondRoundPicksUpMemoryOnExpandedOperation(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	invariant := createMemory(t, env, domain.Memory{
		Type:  domain.MemoryInvariant,
		Scope: domain.ScopeWorkspace,
		Subject: domain.Subject{
			Operation: "rider-service.getRider",
			Field:     "response.200.body.qcomSkill",
		},
		Tags: []string{"qcom", "allocation"},
		Text: "qcomSkill indicates whether the rider is eligible for QCOM (quick-commerce) " +
			"allocation. Implications: QCOM allocation should only select riders with qcomSkill=true.",
	})
	riderOnly := createMemory(t, env, domain.Memory{
		Type:  domain.MemoryTesting,
		Scope: domain.ScopeWorkspace,
		Subject: domain.Subject{
			Operation: "rider-service.getRider",
		},
		Text: "Rider status changes can take a few seconds to propagate after being toggled online.",
	})

	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)
	bundle, err := b.Build(ctx, domain.ContextRequest{Intent: intentQCOMAllocationTest})
	require.NoError(t, err)

	require.NotNil(t, findOp(bundle.Operations, "rider-service.getRider"),
		"expected the invariant memory to pull getRider in, got %+v", bundle.Operations)
	require.NotNil(t, findMemory(bundle.Memories, invariant.ID))
	require.NotNil(t, findMemory(bundle.Memories, riderOnly.ID),
		"expected the second retrieval round to pick up the getRider-only testing memory, got %+v", bundle.Memories)
}

// TestBuild_MemoryExpansion_ErrorSubjectFallback checks subjectOperationID's
// second path: a memory whose subject has no bare Operation but does carry
// an Error ref (PLAN.md §11's error-scoped subject) still expands to
// Error.Operation - while a memory whose Error ref carries no Operation
// either (status/code only) contributes nothing to expansion, proving that
// fallback doesn't fire on an empty Operation.
func TestBuild_MemoryExpansion_ErrorSubjectFallback(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	errWithOp := createMemory(t, env, domain.Memory{
		Type:  domain.MemoryGotcha,
		Scope: domain.ScopeWorkspace,
		Subject: domain.Subject{
			Error: &domain.ErrorRef{
				Operation: "rider-service.getRider",
				Status:    404,
				Code:      "RIDER_NOT_FOUND",
			},
		},
		Tags: []string{"qcom"},
		Text: "riders can vanish mid-allocation; getRider then 404s with RIDER_NOT_FOUND.",
	})
	errNoOp := createMemory(t, env, domain.Memory{
		Type:  domain.MemoryGotcha,
		Scope: domain.ScopeWorkspace,
		Subject: domain.Subject{
			Error: &domain.ErrorRef{Status: 500, Code: "INTERNAL"},
		},
		Tags: []string{"qcom"},
		Text: "a bare 500 with no operation attached carries no expansion target.",
	})

	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)
	bundle, err := b.Build(ctx, domain.ContextRequest{Intent: intentQCOMAllocationTest})
	require.NoError(t, err)

	require.NotNil(t, findMemory(bundle.Memories, errWithOp.ID))
	require.NotNil(t, findMemory(bundle.Memories, errNoOp.ID))
	require.NotNil(t, findOp(bundle.Operations, "rider-service.getRider"),
		"expected the error-subject fallback to add getRider, got %+v", bundle.Operations)
}
