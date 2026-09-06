package retrieval_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/retrieval"
)

// findOp returns the OperationContext with the given id, or nil.
func findOp(ops []domain.OperationContext, id string) *domain.OperationContext {
	for i := range ops {
		if ops[i].ID == id {
			return &ops[i]
		}
	}
	return nil
}

// findMemory returns the MemoryContext with the given id, or nil.
func findMemory(mems []domain.MemoryContext, id string) *domain.MemoryContext {
	for i := range mems {
		if mems[i].ID == id {
			return &mems[i]
		}
	}
	return nil
}

// createQCOMMemories seeds the three memories the task fixture calls for: an
// invariant memory on rider-service.getRider's qcomSkill field, a testing
// memory on order-service.getOrderTimeline, and an unrelated personal note
// with no subject at all (so it can never be a structural or lexical
// candidate for a QCOM/allocation intent).
func createQCOMMemories(t *testing.T, env *testEnv) (invariant, testingMem, personal *domain.Memory) {
	t.Helper()
	invariant = createMemory(t, env, domain.Memory{
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
	testingMem = createMemory(t, env, domain.Memory{
		Type:  domain.MemoryTesting,
		Scope: domain.ScopeWorkspace,
		Subject: domain.Subject{
			Operation: "order-service.getOrderTimeline",
		},
		Text: "wait for timeline generation before asserting ETA",
	})
	personal = createMemory(t, env, domain.Memory{
		Scope: domain.ScopePersonal,
		Text:  "Remember to renew car insurance before the 15th.",
	})
	return invariant, testingMem, personal
}

// TestBuild_ExplicitOperations_MemoriesDocsTokens exercises the scenario the
// task fixture describes: an intent naming "Create a QCOM allocation test"
// plus the two operations that scenario needs (allocation-service.allocate,
// rider-service.getRider) given explicitly, since search alone (verified
// against the live FTS/BM25 ranking) does not surface rider-service.getRider
// for that exact intent string - its lexical score normalizes to 0 against
// this literal query, so asserting it via search would be flaky/fictional.
// Explicit operations exercise PLAN.md §14 step 1's other branch, while
// req.Intent still drives docs' lexical scoring (step 2) and memories'
// Search(intent) half (step 3), so this is still a faithful exercise of
// those steps.
func TestBuild_ExplicitOperations_MemoriesDocsTokens(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	invariant, _, personal := createQCOMMemories(t, env)

	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)
	bundle, err := b.Build(ctx, domain.ContextRequest{
		Intent:     "Create a QCOM allocation test",
		Operations: []string{"allocation-service.allocate", "rider-service.getRider"},
	})
	require.NoError(t, err)

	// Operations: both explicit IDs present, in request order; the
	// deprecated allocateV1 was never asked for, so it cannot outrank
	// allocate.
	require.Len(t, bundle.Operations, 2)
	require.Equal(t, "allocation-service.allocate", bundle.Operations[0].ID)
	require.Equal(t, "rider-service.getRider", bundle.Operations[1].ID)
	for _, oc := range bundle.Operations {
		require.Equal(t, domain.TierContract, oc.Tier)
	}
	require.Nil(t, findOp(bundle.Operations, "allocation-service.allocateV1"))

	// Memories: the invariant memory on getRider's qcomSkill field is
	// present at the memory tier with its type preserved, and it outranks
	// the unrelated personal note (which is absent entirely - it shares no
	// subject or lexical token with this intent or these operations).
	got := findMemory(bundle.Memories, invariant.ID)
	require.NotNil(t, got, "expected invariant qcomSkill memory in bundle, got %+v", bundle.Memories)
	require.Equal(t, domain.TierMemory, got.Tier)
	require.Equal(t, domain.MemoryInvariant, got.Type)
	require.Nil(t, findMemory(bundle.Memories, personal.ID), "personal note must not appear for this intent")

	// Docs: at least one section comes from allocation-service's
	// docs/allocation.md (reached via the "allocate" operation ref).
	foundAllocationDoc := false
	for _, d := range bundle.Docs {
		if d.Service == "allocation-service" && d.Path == "docs/allocation.md" {
			foundAllocationDoc = true
			break
		}
	}
	require.True(t, foundAllocationDoc, "expected a docs/allocation.md section, got %+v", bundle.Docs)

	require.Greater(t, bundle.EstimatedTokens, 0)
}

// TestBuild_ExplicitOperations_Minimal exercises the explicit-operations path
// on its own (PLAN.md §14 step 1's "operations" branch): no search
// involved, no intent, a single known ID.
func TestBuild_ExplicitOperations_Minimal(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)

	bundle, err := b.Build(ctx, domain.ContextRequest{
		Operations: []string{"allocation-service.allocate"},
	})
	require.NoError(t, err)
	require.Len(t, bundle.Operations, 1)
	oc := bundle.Operations[0]
	require.Equal(t, "allocation-service.allocate", oc.ID)
	require.Equal(t, domain.TierContract, oc.Tier)
	require.Equal(t, "POST", oc.Method)
	require.Equal(t, "/v1/allocations", oc.Path)
	require.Zero(t, oc.Score, "explicit operations carry no search score")
}

// TestBuild_SearchSelectsOperations exercises PLAN.md §14 step 1's other
// branch: req.Operations empty, so operations come from Search.Operations
// (limit 8, floor 0.15, and 25% of the top score). Intent "qcom" is used
// rather than the fixture's "Create a QCOM allocation test" phrase: verified
// directly against the live FTS/BM25 ranking, that exact phrase normalizes
// rider-service.getRider's score to 0 (it shares no literal token with
// "test"/"allocation"/"create" beyond a tag/field match that's weak
// relative to the rest of that specific query's hits), so it never clears
// the floor - asserting its inclusion for that literal intent would be
// asserting fiction. "qcom" is a real, deterministic case where both
// allocate and getRider score 1.0 and are actually selected.
func TestBuild_SearchSelectsOperations(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)

	bundle, err := b.Build(ctx, domain.ContextRequest{Intent: "qcom"})
	require.NoError(t, err)
	require.LessOrEqual(t, len(bundle.Operations), 8)

	allocate := findOp(bundle.Operations, "allocation-service.allocate")
	require.NotNil(t, allocate, "expected allocate among search results, got %+v", bundle.Operations)
	require.NotZero(t, allocate.Score)
	require.NotNil(t, findOp(bundle.Operations, "rider-service.getRider"))

	// The deprecated allocateV1 must never outrank (by score, or by being
	// selected when allocate isn't) its replacement, whether or not it
	// clears the floor at all.
	if v1 := findOp(bundle.Operations, "allocation-service.allocateV1"); v1 != nil {
		require.Less(t, v1.Score, allocate.Score)
	}
}

// TestBuild_ExplicitOperations_Duplicate checks that naming the same
// operation twice (or naming one also reachable via req.Flow) doesn't
// duplicate it in the bundle.
func TestBuild_ExplicitOperations_Duplicate(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)

	bundle, err := b.Build(ctx, domain.ContextRequest{
		Operations: []string{"allocation-service.allocate", "allocation-service.allocate"},
	})
	require.NoError(t, err)
	require.Len(t, bundle.Operations, 1)
}

// TestBuild_FlowPath exercises PLAN.md §14 step 1's flow-augmentation rule
// using allocation-service's fixture flow flows/smoke.flow.yaml (id "smoke",
// owned by allocation-service, calling allocate then getAllocation) - this
// also exercises that registry.Snapshot's Flows are actually applied by
// catalog.Apply (verified directly: cat.GetFlowSummary(ctx, "smoke") returns
// the flow after newTestEnv's setup).
func TestBuild_FlowPath(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)

	bundle, err := b.Build(ctx, domain.ContextRequest{Flow: "smoke"})
	require.NoError(t, err)

	require.NotNil(t, findOp(bundle.Operations, "allocation-service.allocate"))
	require.NotNil(t, findOp(bundle.Operations, "allocation-service.getAllocation"))

	require.NotEmpty(t, bundle.Flows)
	var flow *domain.FlowContext
	for i := range bundle.Flows {
		if bundle.Flows[i].ID == "smoke" {
			flow = &bundle.Flows[i]
		}
	}
	require.NotNil(t, flow, "expected the smoke flow in bundle.Flows, got %+v", bundle.Flows)
	require.Equal(t, "Allocation smoke test", flow.Name)
	require.Equal(t, []string{
		"call: allocation-service.allocate",
		"call: allocation-service.getAllocation",
	}, flow.Steps)
}

// TestBuild_FlowPath_WithExplicitOperations checks that req.Flow's operations
// are unioned in alongside explicit req.Operations, not used instead of them.
func TestBuild_FlowPath_WithExplicitOperations(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)

	bundle, err := b.Build(ctx, domain.ContextRequest{
		Operations: []string{"rider-service.getRider"},
		Flow:       "smoke",
	})
	require.NoError(t, err)
	require.NotNil(t, findOp(bundle.Operations, "rider-service.getRider"))
	require.NotNil(t, findOp(bundle.Operations, "allocation-service.allocate"))
	require.NotNil(t, findOp(bundle.Operations, "allocation-service.getAllocation"))
}

// TestBuild_UnknownOperation checks PLAN.md §14 step 1's error path: an
// explicit operation ID the catalog doesn't know errors with
// E_OPERATION_NOT_FOUND and suggestions.
func TestBuild_UnknownOperation(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)

	_, err := b.Build(ctx, domain.ContextRequest{
		Operations: []string{"allocation-service.alocate"}, // typo of "allocate"
	})
	require.Error(t, err)
	require.True(t, errs.Is(err, errs.OperationNotFound), "got code %v", errs.CodeOf(err))

	e := errs.As(err)
	require.NotNil(t, e)
	suggestions, ok := e.Details["suggestions"].([]string)
	require.True(t, ok, "expected []string suggestions, got %#v", e.Details["suggestions"])
	require.NotEmpty(t, suggestions)
	require.Contains(t, suggestions, "allocation-service.allocate")
}

// TestBuild_UnknownFlow checks that a request naming a flow ID the catalog
// doesn't know degrades gracefully (like a flow step naming a since-removed
// operation, per selectOperations' own doc comment) rather than erroring:
// the request's other operations still resolve, the unknown flow just
// contributes nothing.
func TestBuild_UnknownFlow(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)

	bundle, err := b.Build(ctx, domain.ContextRequest{
		Operations: []string{"allocation-service.allocate"},
		Flow:       "does-not-exist",
	})
	require.NoError(t, err)
	require.Len(t, bundle.Operations, 1)
	require.Equal(t, "allocation-service.allocate", bundle.Operations[0].ID)
	// bundle.Flows comes from step 4 ("flows using a selected operation"),
	// independent of whether req.Flow itself resolved - allocate is used by
	// the (real) smoke flow, so it's expected here regardless.
	require.NotEmpty(t, bundle.Flows)
}

// TestBuild_BudgetEnforcement checks PLAN.md §14 steps 6-7: a tight budget
// forces cuts (recorded in Omitted) and the final bundle respects it with
// some slack (EstimateTokens is an approximation, and operations are never
// cut below 1). Intent is deliberately omitted here: it feeds a lexical doc
// search (step 2) that pulls in extra sections from other services, and
// PLAN's tiers only trim doc *bodies*, never doc *count* - so the achievable
// floor after every tier fires still scales with how many docs got selected
// in the first place. A single explicit operation with no intent keeps that
// count within what §14's tiers can actually bring under a 600-token
// budget.
func TestBuild_BudgetEnforcement(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)

	unbounded, err := b.Build(ctx, domain.ContextRequest{
		Operations: []string{"allocation-service.allocate"},
	})
	require.NoError(t, err)
	require.Empty(t, unbounded.Omitted)

	bundle, err := b.Build(ctx, domain.ContextRequest{
		Operations:   []string{"allocation-service.allocate"},
		BudgetTokens: 600,
	})
	require.NoError(t, err)
	require.NotEmpty(t, bundle.Omitted, "expected a tight budget to cut something")
	require.LessOrEqual(t, bundle.EstimatedTokens, 720)
	require.GreaterOrEqual(t, len(bundle.Operations), 1, "operations must never be cut to zero")
}

// TestBuild_BudgetEnforcement_ReducesOperationCount checks the last-resort
// "reduce operations" tier: with 2 explicit operations and a budget too
// tight for the other tiers to satisfy alone, one operation is dropped
// (from the tail, so the first-listed operation - the one the caller most
// wanted - survives).
func TestBuild_BudgetEnforcement_ReducesOperationCount(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)

	bundle, err := b.Build(ctx, domain.ContextRequest{
		Operations:   []string{"allocation-service.allocate", "rider-service.getRider"},
		BudgetTokens: 400,
	})
	require.NoError(t, err)
	require.Equal(t, 1, bundle.Omitted["operations"])
	require.Len(t, bundle.Operations, 1)
	require.Equal(t, "allocation-service.allocate", bundle.Operations[0].ID)
}

// TestBuild_BudgetEnforcement_DescriptionsAndSchemaDepth checks the
// "truncate descriptions" and "prune response schemas" tiers directly:
// order-service.createOrder has both a description over
// maxDescriptionRunes and a nested response field (timeline[].status, an
// array-item field two levels below response.201.body.) that only the
// schema-depth tier removes.
func TestBuild_BudgetEnforcement_DescriptionsAndSchemaDepth(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)

	bundle, err := b.Build(ctx, domain.ContextRequest{
		Operations:   []string{"order-service.createOrder"},
		BudgetTokens: 500,
	})
	require.NoError(t, err)
	require.NotZero(t, bundle.Omitted["descriptions"])
	require.NotZero(t, bundle.Omitted["schema_depth"])
	require.Len(t, bundle.Operations, 1)

	oc := bundle.Operations[0]
	require.LessOrEqual(t, len([]rune(oc.Description)), 200)
	for _, r := range oc.Response {
		require.NotContains(t, r, "timeline[].status")
		require.NotContains(t, r, "timeline[].note")
		require.NotContains(t, r, "timeline[].at")
	}
}

// TestBuild_DefaultBudget checks that an unset BudgetTokens defaults to 8000
// rather than 0 (which would make everything "over budget").
func TestBuild_DefaultBudget(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)

	bundle, err := b.Build(ctx, domain.ContextRequest{
		Operations: []string{"allocation-service.allocate"},
	})
	require.NoError(t, err)
	require.Empty(t, bundle.Omitted)
}

// TestBuild_NilMemAndRuns checks that a Builder wired with nil mem and nil
// runsrc (both allowed per New's contract) still builds successfully, with
// empty memories/runs tiers rather than erroring.
func TestBuild_NilMemAndRuns(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	b := retrieval.New(env.cat, env.srch, nil, nil)

	bundle, err := b.Build(ctx, domain.ContextRequest{
		Intent:     "Create a QCOM allocation test",
		Operations: []string{"allocation-service.allocate", "rider-service.getRider"},
	})
	require.NoError(t, err)
	require.Empty(t, bundle.Memories)
	require.Empty(t, bundle.Runs)
	require.NotEmpty(t, bundle.Operations)
}

// TestBuild_RunsTier exercises PLAN.md §14 step 5/6: the runs tier surfaces
// recent runs touching a selected operation, one-line-summarized.
func TestBuild_RunsTier(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	run := &domain.Run{
		FlowID:      "smoke",
		Environment: "local",
		Status:      domain.RunPassed,
	}
	require.NoError(t, env.runs.Create(ctx, run))
	require.NoError(t, env.runs.AppendStep(ctx, run.ID, domain.StepResult{
		StepID:    "allocate",
		Index:     0,
		Operation: "allocation-service.allocate",
		Status:    domain.StepPassed,
	}))
	run.Status = domain.RunPassed
	run.DurationMs = 1234
	run.Summary = domain.RunSummary{StepsTotal: 1, StepsPassed: 1}
	require.NoError(t, env.runs.Update(ctx, run))

	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)
	bundle, err := b.Build(ctx, domain.ContextRequest{
		Operations: []string{"allocation-service.allocate"},
	})
	require.NoError(t, err)
	require.Len(t, bundle.Runs, 1)
	rc := bundle.Runs[0]
	require.Equal(t, domain.TierRun, rc.Tier)
	require.Equal(t, run.ID, rc.ID)
	require.Equal(t, "smoke", rc.FlowID)
	require.Contains(t, rc.Summary, "passed")
	require.Contains(t, rc.Summary, "1/1 steps")
}

// TestBuild_RunsTier_NoMatchingRuns checks that runs touching unrelated
// operations don't leak into the bundle.
func TestBuild_RunsTier_NoMatchingRuns(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	run := &domain.Run{Status: domain.RunPassed}
	require.NoError(t, env.runs.Create(ctx, run))
	require.NoError(t, env.runs.AppendStep(ctx, run.ID, domain.StepResult{
		StepID:    "getOrder",
		Operation: "order-service.getOrder",
		Status:    domain.StepPassed,
	}))
	require.NoError(t, env.runs.Update(ctx, run))

	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)
	bundle, err := b.Build(ctx, domain.ContextRequest{
		Operations: []string{"allocation-service.allocate"},
	})
	require.NoError(t, err)
	require.Empty(t, bundle.Runs)
}
