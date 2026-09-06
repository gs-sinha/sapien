package retrieval_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/retrieval"
)

// fakeExampleSource is a minimal retrieval.ExampleSource for exercising the
// examples tier: byOp holds the (unsorted) saved examples per operation id,
// and ForOperations sorts verified-first (mirroring the real ExampleAPI's
// documented contract) and applies limit, exactly like the production
// implementation is expected to. A non-nil err makes every call fail, to
// exercise Build's tolerance of an example source that isn't wired up yet.
type fakeExampleSource struct {
	byOp map[string][]domain.SavedExample
	err  error
}

func (f *fakeExampleSource) ForOperations(_ context.Context, ids []string, limit int) ([]domain.SavedExample, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []domain.SavedExample
	for _, id := range ids {
		exs := append([]domain.SavedExample{}, f.byOp[id]...)
		sort.SliceStable(exs, func(i, j int) bool {
			vi, vj := exs[i].Verified != nil, exs[j].Verified != nil
			return vi && !vj
		})
		if limit > 0 && len(exs) > limit {
			exs = exs[:limit]
		}
		out = append(out, exs...)
	}
	return out, nil
}

// findExample returns the ExampleContext with the given id, or nil.
func findExample(exs []domain.ExampleContext, id string) *domain.ExampleContext {
	for i := range exs {
		if exs[i].ID == id {
			return &exs[i]
		}
	}
	return nil
}

// TestBuild_ExamplesTier_VerifiedFirstPerOperationCapAndMultiOp checks the
// examples tier end to end: it is gathered for every selected operation (not
// just the first), capped at 2 per operation even when more are available,
// verified examples sort ahead of unverified ones, and Verified/Env are
// flattened out of SavedExample.Verified onto the ExampleContext.
func TestBuild_ExamplesTier_VerifiedFirstPerOperationCapAndMultiOp(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	draft := domain.SavedExample{ID: "draft", Operation: "allocation-service.allocate", Description: "hand-written draft"}
	verified := domain.SavedExample{
		ID: "verified", Operation: "allocation-service.allocate", Description: "known-good",
		Verified: &domain.ExampleVerified{Env: "staging", RunID: "run_1"},
	}
	extra := domain.SavedExample{ID: "extra", Operation: "allocation-service.allocate"}
	riderEx := domain.SavedExample{
		ID: "rider-ex", Operation: "rider-service.getRider",
		Verified: &domain.ExampleVerified{Env: "qa"},
	}

	src := &fakeExampleSource{byOp: map[string][]domain.SavedExample{
		// Seeded in draft/verified/extra order so a passing test proves the
		// tier actually sorts verified-first rather than happening to
		// receive them that way; three seeded, only 2 should ever surface.
		"allocation-service.allocate": {draft, verified, extra},
		"rider-service.getRider":      {riderEx},
	}}

	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)
	b.SetExamples(src)

	bundle, err := b.Build(ctx, domain.ContextRequest{
		Operations: []string{"allocation-service.allocate", "rider-service.getRider"},
	})
	require.NoError(t, err)

	var allocateExamples []domain.ExampleContext
	for _, ec := range bundle.Examples {
		if ec.Operation == "allocation-service.allocate" {
			allocateExamples = append(allocateExamples, ec)
		}
	}
	require.Len(t, allocateExamples, 2, "per-operation cap of 2 must apply even though 3 examples exist")
	assert.Equal(t, "verified", allocateExamples[0].ID, "verified examples must sort ahead of unverified ones")
	assert.True(t, allocateExamples[0].Verified)
	assert.Equal(t, "staging", allocateExamples[0].Env)

	rider := findExample(bundle.Examples, "rider-ex")
	require.NotNil(t, rider, "examples must be gathered across every selected operation, not just the first")
	assert.True(t, rider.Verified)
	assert.Equal(t, "qa", rider.Env)
}

// TestBuild_ExamplesTier_NilSourceAndErrorsAreTolerated checks PLAN's
// nil-safety requirement two ways: a Builder that never had SetExamples
// called renders an empty tier (not an error), and once a source is set, an
// error from it (e.g. E_NOT_IMPLEMENTED before a workspace's example store
// is wired in) degrades the tier to empty rather than failing the whole
// context bundle.
func TestBuild_ExamplesTier_NilSourceAndErrorsAreTolerated(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)
	bundle, err := b.Build(ctx, domain.ContextRequest{Operations: []string{"allocation-service.allocate"}})
	require.NoError(t, err)
	require.Empty(t, bundle.Examples, "no example source configured: the tier must stay empty, not error")

	b.SetExamples(&fakeExampleSource{err: errors.New("store not wired yet")})
	bundle2, err := b.Build(ctx, domain.ContextRequest{Operations: []string{"allocation-service.allocate"}})
	require.NoError(t, err, "an example source error must not fail the whole bundle")
	require.Empty(t, bundle2.Examples)

	b.SetExamples(nil)
	bundle3, err := b.Build(ctx, domain.ContextRequest{Operations: []string{"allocation-service.allocate"}})
	require.NoError(t, err)
	require.Empty(t, bundle3.Examples, "SetExamples(nil) must clear the source back to no-tier")
}

// TestBuild_ExamplesTier_CutBeforeOtherTiers checks PLAN.md §14 step 7's
// tier order for the new examples tier: a budget just above the bundle's
// size *without* examples, but well below its size *with* them, is
// satisfied by dropping the examples tier alone -- no doc body, description,
// schema-depth, or operation-count tier is ever touched.
func TestBuild_ExamplesTier_CutBeforeOtherTiers(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	heavyBody := map[string]any{"note": strings.Repeat("x", 400)}
	src := &fakeExampleSource{byOp: map[string][]domain.SavedExample{
		"allocation-service.allocate": {{ID: "ex1", Operation: "allocation-service.allocate", Body: heavyBody}},
	}}

	b := retrieval.New(env.cat, env.srch, env.mem, env.runs)
	req := domain.ContextRequest{Operations: []string{"allocation-service.allocate"}, BudgetTokens: 100000}

	baseline, err := b.Build(ctx, req)
	require.NoError(t, err)
	require.Empty(t, baseline.Examples)
	noExamplesTokens := baseline.EstimatedTokens

	b.SetExamples(src)
	withExamples, err := b.Build(ctx, req)
	require.NoError(t, err)
	require.NotEmpty(t, withExamples.Examples)
	require.Greater(t, withExamples.EstimatedTokens, noExamplesTokens, "the heavy example body must actually grow the bundle")

	tight, err := b.Build(ctx, domain.ContextRequest{
		Operations:   []string{"allocation-service.allocate"},
		BudgetTokens: noExamplesTokens + 30,
	})
	require.NoError(t, err)
	require.Empty(t, tight.Examples, "examples must be the tier a tight-but-sufficient budget cuts")
	require.Len(t, tight.Omitted, 1, "dropping examples alone must satisfy this budget; nothing else may be cut, got %+v", tight.Omitted)
	require.Contains(t, tight.Omitted, "examples")
}
