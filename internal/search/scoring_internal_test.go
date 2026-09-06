package search

// Internal (white-box) tests for the search ranking tuning task's core
// scoring primitives: proportional normalization (never per-id clamping)
// and the deterministic tie-break. These are tested directly against
// finalizeLexicalScores/normalizeScores rather than through Operations(),
// because real bm25 is sensitive to unrelated text (path length, column
// content) in ways that make it hard to construct a genuine, controlled
// tie end-to-end — see operations_test.go / TestOperations_* for the
// integration-level coverage of the same behavior against real FTS data.

import (
	"testing"

	"github.com/growsimplee/sapien/internal/domain"
)

func TestFinalizeLexicalScores_ProportionalNotClamped(t *testing.T) {
	list := []scoredID{
		{id: "c", score: 1},
		{id: "a", score: 4},
		{id: "b", score: 2},
	}

	out := finalizeLexicalScores(list, 10, nil, map[string]opMeta{}, map[string]domain.Operation{})

	if len(out) != 3 {
		t.Fatalf("expected 3 results, got %d", len(out))
	}
	want := []struct {
		id    string
		score float64
	}{
		{"a", 1.0},  // best raw score normalizes to exactly 1.0
		{"b", 0.5},  // proportional, not clamped
		{"c", 0.25}, // proportional, not clamped
	}
	for i, w := range want {
		if out[i].id != w.id {
			t.Fatalf("index %d: got id %q, want %q (full order: %v)", i, out[i].id, w.id, out)
		}
		if out[i].score != w.score {
			t.Fatalf("index %d (%s): got score %v, want %v", i, w.id, out[i].score, w.score)
		}
	}
	// Strictly descending: distinct values, not a flattened tie.
	for i := 1; i < len(out); i++ {
		if out[i].score >= out[i-1].score {
			t.Fatalf("scores not strictly descending at index %d: %v", i, out)
		}
	}
}

func TestFinalizeLexicalScores_LimitTruncatesAfterSort(t *testing.T) {
	list := []scoredID{
		{id: "z", score: 1},
		{id: "y", score: 3},
		{id: "x", score: 2},
	}
	out := finalizeLexicalScores(list, 2, nil, map[string]opMeta{}, map[string]domain.Operation{})
	if len(out) != 2 || out[0].id != "y" || out[1].id != "x" {
		t.Fatalf("got %v, want [y, x]", out)
	}
}

func TestFinalizeLexicalScores_TieBreak_ExactRawOpIDWinsOutright(t *testing.T) {
	tokens := []string{"widget"}
	metas := map[string]opMeta{
		"svc-a.opA": {rawOpID: "opA", serviceName: "svc-a"},
		"svc-b.opB": {rawOpID: "opB", serviceName: "svc-b"},
		"svc-c.opC": {rawOpID: "widget", serviceName: "svc-c"}, // exact raw_op_id token match
	}
	ops := map[string]domain.Operation{
		"svc-a.opA": {HTTP: &domain.HTTPBinding{Path: "/v1/aaaaaaaaaa"}}, // longer path
		"svc-b.opB": {HTTP: &domain.HTTPBinding{Path: "/v1/b"}},          // shorter path
		"svc-c.opC": {HTTP: &domain.HTTPBinding{Path: "/v1/cccccccccc"}}, // longest path, but exact match
	}
	list := []scoredID{
		{id: "svc-a.opA", score: 5},
		{id: "svc-b.opB", score: 5},
		{id: "svc-c.opC", score: 5},
	}

	out := finalizeLexicalScores(list, 10, tokens, metas, ops)

	for _, sc := range out {
		if sc.score != 1.0 {
			t.Fatalf("tied raw scores should all normalize to 1.0, got %v for %s", sc.score, sc.id)
		}
	}
	got := []string{out[0].id, out[1].id, out[2].id}
	want := []string{"svc-c.opC", "svc-b.opB", "svc-a.opA"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tie-break order = %v, want %v", got, want)
		}
	}
}

func TestFinalizeLexicalScores_TieBreak_ShorterPathWins(t *testing.T) {
	tokens := []string{"widget"} // matches neither op's rawOpID, so this tier is skipped
	metas := map[string]opMeta{
		"svc-a.opA": {rawOpID: "opA", serviceName: "svc-a"},
		"svc-b.opB": {rawOpID: "opB", serviceName: "svc-b"},
	}
	ops := map[string]domain.Operation{
		"svc-a.opA": {HTTP: &domain.HTTPBinding{Path: "/v1/a-much-longer-path/{id}"}},
		"svc-b.opB": {HTTP: &domain.HTTPBinding{Path: "/v1/b"}},
	}
	list := []scoredID{
		{id: "svc-a.opA", score: 2},
		{id: "svc-b.opB", score: 2},
	}

	out := finalizeLexicalScores(list, 10, tokens, metas, ops)
	if out[0].id != "svc-b.opB" || out[1].id != "svc-a.opA" {
		t.Fatalf("expected shorter path (svc-b.opB) first, got %v", out)
	}
}

func TestFinalizeLexicalScores_TieBreak_IDAscendingFinalKey(t *testing.T) {
	tokens := []string{"widget"}
	metas := map[string]opMeta{
		"mmm-service.op": {rawOpID: "op", serviceName: "mmm-service"},
		"bbb-service.op": {rawOpID: "op", serviceName: "bbb-service"},
	}
	ops := map[string]domain.Operation{
		"mmm-service.op": {HTTP: &domain.HTTPBinding{Path: "/v1/x"}},
		"bbb-service.op": {HTTP: &domain.HTTPBinding{Path: "/v1/x"}},
	}
	list := []scoredID{
		{id: "mmm-service.op", score: 3},
		{id: "bbb-service.op", score: 3},
	}

	out := finalizeLexicalScores(list, 10, tokens, metas, ops)
	if out[0].id != "bbb-service.op" || out[1].id != "mmm-service.op" {
		t.Fatalf("expected id-ascending order [bbb-service.op, mmm-service.op], got %v", out)
	}
}

func TestFinalizeLexicalScores_EmptyListIsNoop(t *testing.T) {
	out := finalizeLexicalScores(nil, 10, nil, nil, nil)
	if out != nil {
		t.Fatalf("expected nil for an empty input, got %v", out)
	}
}

func TestPathLen_NonHTTPOperationSortsLast(t *testing.T) {
	if pathLen(domain.Operation{HTTP: &domain.HTTPBinding{Path: "/v1/x"}}) != len("/v1/x") {
		t.Fatalf("expected pathLen to count the HTTP path's runes")
	}
	nonHTTP := pathLen(domain.Operation{})
	if nonHTTP <= len("/v1/x") {
		t.Fatalf("expected a non-HTTP operation to get a sentinel larger than any real path, got %d", nonHTTP)
	}
}

func TestDescriptionServiceStemBoost_EmptyServiceNameIsZero(t *testing.T) {
	op := domain.Operation{Summary: "Allocate a rider", Description: "Finds a rider."}
	if got := descriptionServiceStemBoost([]string{"allocate"}, op, ""); got != 0 {
		t.Fatalf("expected 0 boost with no service name, got %v", got)
	}
}

func TestNormalizeScores_NonPositiveBestLeavesScoresUnchanged(t *testing.T) {
	list := []scoredID{{id: "a", score: 0}, {id: "b", score: 0}}
	normalizeScores(list)
	if list[0].score != 0 || list[1].score != 0 {
		t.Fatalf("expected scores to stay 0 when best is non-positive, got %v", list)
	}
}

func TestTokenWeight_GenericVerbsAreDownweighted(t *testing.T) {
	for _, v := range []string{"create", "get", "list", "find", "fetch", "show", "search", "test", "verify", "check", "make", "new", "run"} {
		if w := tokenWeight(v); w != genericVerbWeight {
			t.Errorf("tokenWeight(%q) = %v, want %v", v, w, genericVerbWeight)
		}
	}
	for _, n := range []string{"allocation", "qcom", "rider", "order"} {
		if w := tokenWeight(n); w != 1.0 {
			t.Errorf("tokenWeight(%q) = %v, want 1.0 (domain noun keeps full weight)", n, w)
		}
	}
}

func TestStemMatch(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"allocate", "allocation", true}, // shared "allocat" root, length 7 >= stemMinRunes
		{"order", "orders", true},        // "order" is a full common prefix, length 5
		{"qcom", "qcom", true},           // exact match always matches
		{"qcom", "allocation", false},    // unrelated, no shared prefix
		{"id", "identifier", false},      // too short to safely stem
		{"", "allocate", false},          // empty never matches
	}
	for _, tc := range cases {
		if got := stemMatch(tc.a, tc.b); got != tc.want {
			t.Errorf("stemMatch(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
