package search_test

// Tests for the search ranking tuning task's usage-feedback loop (PLAN §16,
// task part 2): search_feedback rows add a small, capped boost on top of
// lexical scoring and matched_on gains a "feedback" label.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/search"
)

func TestFeedbackTokens(t *testing.T) {
	tests := []struct {
		name  string
		query string
		max   int
		want  []string
	}{
		{"drops stop words and generic verbs", "find riders around a pickup", 0, []string{"riders", "around", "pickup"}},
		{"a bare generic verb still yields nothing extra", "create", 0, []string{}},
		{"dedupes, keeps first occurrence order", "allocate a rider allocate", 0, []string{"allocate", "rider"}},
		{"caps at max", "alpha bravo charlie delta echo", 2, []string{"alpha", "bravo"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := search.FeedbackTokens(tt.query, tt.max)
			assert.Equal(t, tt.want, got)
		})
	}
}

// feedbackTieFixture is two operations with identical summary,
// description, tags, concepts, and equal-length path/rawOpID — a
// deliberately exact lexical tie, so any ranking difference is
// attributable purely to the feedback boost under test.
func feedbackTieFixture() []opFixture {
	base := opFixture{
		serviceID: "svc_x", serviceName: "x-service",
		summary: "Handle a widget", description: "Handles a widget request.",
		tags: []string{"widgets"}, concepts: []string{"widgets"},
	}
	a := base
	a.id, a.method, a.path, a.rawOpID = "x-service.opAlpha", "GET", "/v1/widgets/alpha", "opAlpha"
	b := base
	b.id, b.method, b.path, b.rawOpID = "x-service.opBravo", "GET", "/v1/widgets/bravo", "opBravo"
	return []opFixture{a, b}
}

func TestOperations_FeedbackBreaksATieAndLabelsMatchedOn(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedOperations(t, db, feedbackTieFixture())

	baseline, err := search.New(db).Operations(ctx, "handle a widget request", domain.SearchOptions{})
	require.NoError(t, err)
	require.Len(t, baseline, 2)
	assert.Equal(t, baseline[0].Score, baseline[1].Score, "identical fixtures: an exact tie")
	assert.Equal(t, "x-service.opAlpha", baseline[0].Operation.ID, "id-ascending tie-break with no other signal")

	seedFeedback(t, db, []feedbackFixture{
		{term: "widget", opID: "x-service.opBravo", count: 5},
	})

	results, err := search.New(db).Operations(ctx, "handle a widget request", domain.SearchOptions{})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "x-service.opBravo", results[0].Operation.ID, "feedback must flip the tie")
	assert.Equal(t, 1.0, results[0].Score)
	assert.Contains(t, results[0].MatchedOn, "feedback")
	assert.NotContains(t, results[1].MatchedOn, "feedback")
	assert.Less(t, results[1].Score, results[0].Score)
}

func TestOperations_FeedbackBoostIsCapped(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedOperations(t, db, feedbackTieFixture())

	seedFeedback(t, db, []feedbackFixture{
		// Already well past saturation at this fixture's scale (verified
		// empirically: log1p(50) alone already exceeds the cap here).
		{term: "widget", opID: "x-service.opBravo", count: 50},
	})
	moderate, err := search.New(db).Operations(ctx, "handle a widget request", domain.SearchOptions{})
	require.NoError(t, err)

	db2 := openTestDB(t)
	seedOperations(t, db2, feedbackTieFixture())
	seedFeedback(t, db2, []feedbackFixture{
		// A vastly larger count: log1p grows slowly, but the point of the
		// cap is that it doesn't matter how large the raw signal gets once
		// saturated — the loser's score (opAlpha, relative to the new top)
		// must land on the exact same capped ratio as the moderate count
		// above, not keep shrinking.
		{term: "widget", opID: "x-service.opBravo", count: 100000},
	})
	extreme, err := search.New(db2).Operations(ctx, "handle a widget request", domain.SearchOptions{})
	require.NoError(t, err)

	idxModerate := indexOf(moderate, "x-service.opAlpha")
	idxExtreme := indexOf(extreme, "x-service.opAlpha")
	require.GreaterOrEqual(t, idxModerate, 0)
	require.GreaterOrEqual(t, idxExtreme, 0)

	// The cap is relative to the pre-feedback top score, which is the same
	// in both cases (an exact tie): opAlpha's normalized score after
	// opBravo wins must land on the same capped value regardless of how
	// far past the count needed to hit the cap the feedback count goes.
	assert.InDelta(t, moderate[idxModerate].Score, extreme[idxExtreme].Score, 1e-9,
		"the loser's score must not keep shrinking once the cap is saturated: moderate=%+v extreme=%+v", moderate, extreme)
}

func TestOperations_FeedbackAddsNewCandidateAboveThreshold(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// An operation sharing no vocabulary at all with the query.
	fixtures := []opFixture{
		{
			id: "widget-service.rotateWidget", serviceID: "svc_widget", serviceName: "widget-service",
			method: "POST", path: "/v1/widgets/{id}/rotate", rawOpID: "rotateWidget",
			summary: "Rotate a widget", description: "Applies a rotation to a widget.",
			tags: []string{"widgets"},
		},
	}
	seedOperations(t, db, fixtures)

	baseline, err := search.New(db).Operations(ctx, "gronk a fizzbuzz", domain.SearchOptions{})
	require.NoError(t, err)
	assert.Empty(t, baseline, "no lexical signal at all for an unrelated query")

	// Below the threshold: still nothing.
	seedFeedback(t, db, []feedbackFixture{{term: "fizzbuzz", opID: "widget-service.rotateWidget", count: 2}})
	stillNone, err := search.New(db).Operations(ctx, "gronk a fizzbuzz", domain.SearchOptions{})
	require.NoError(t, err)
	assert.Empty(t, stillNone, "below feedbackNewCandidateMinCount, must not be added")

	// At/above the threshold: added purely on usage.
	db2 := openTestDB(t)
	seedOperations(t, db2, fixtures)
	seedFeedback(t, db2, []feedbackFixture{{term: "fizzbuzz", opID: "widget-service.rotateWidget", count: 3}})
	promoted, err := search.New(db2).Operations(ctx, "gronk a fizzbuzz", domain.SearchOptions{})
	require.NoError(t, err)
	require.Len(t, promoted, 1)
	assert.Equal(t, "widget-service.rotateWidget", promoted[0].Operation.ID)
	assert.Contains(t, promoted[0].MatchedOn, "feedback")
}
