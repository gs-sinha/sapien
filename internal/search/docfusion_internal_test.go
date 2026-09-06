package search

// Unit tests for the docs-fusion experiment's flag parsing and fusion
// arithmetic (search ranking tuning task). These deliberately avoid
// SAPIEN_SEARCH_DOC_FUSION / os.Getenv entirely — see docFusionOverride's
// comment for why an env-var-driven Go subtest would be unreliable here
// (docFusionEnvOnce, mirroring query.go's knowledgeWeights, is read once
// per test binary) — and test the pure functions directly instead.

import (
	"reflect"
	"testing"

	"github.com/gs-sinha/sapien/internal/domain"
)

func TestParseDocFusionConfig(t *testing.T) {
	cases := []struct {
		in         string
		wantMode   docFusionMode
		wantWeight float64
	}{
		{"", docFusionWeighted, 0.7},
		{"off", docFusionOff, 0},
		{"OFF", docFusionOff, 0},
		{"  off  ", docFusionOff, 0},
		{"rrf", docFusionRRF, 0},
		{"RRF", docFusionRRF, 0},
		{"  rrf  ", docFusionRRF, 0},
		{"weighted:0.5", docFusionWeighted, 0.5},
		{"weighted:0.6", docFusionWeighted, 0.6},
		{"weighted:0.7", docFusionWeighted, 0.7},
		{"weighted:0", docFusionWeighted, 0},
		{"weighted:1", docFusionWeighted, 1},
		{"WEIGHTED:0.5", docFusionWeighted, 0.5},
		{"weighted: 0.5", docFusionWeighted, 0.5},
		// malformed/out-of-range -> off, never a crash or a silent
		// out-of-bounds weight.
		{"weighted:1.5", docFusionWeighted, 0.7},
		{"weighted:-0.1", docFusionWeighted, 0.7},
		{"weighted:abc", docFusionWeighted, 0.7},
		{"weighted:", docFusionWeighted, 0.7},
		{"bogus", docFusionWeighted, 0.7},
		{"rrfx", docFusionWeighted, 0.7},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parseDocFusionConfig(tc.in)
			if got.mode != tc.wantMode {
				t.Fatalf("parseDocFusionConfig(%q).mode = %v, want %v", tc.in, got.mode, tc.wantMode)
			}
			if got.mode == docFusionWeighted && got.weight != tc.wantWeight {
				t.Fatalf("parseDocFusionConfig(%q).weight = %v, want %v", tc.in, got.weight, tc.wantWeight)
			}
		})
	}
}

func TestParseDocFusionScope(t *testing.T) {
	cases := []struct {
		in   string
		want docFusionScope
	}{
		{"", docFusionScopeAll},
		{"all", docFusionScopeAll},
		{"ALL", docFusionScopeAll},
		{"heading", docFusionScopeHeading},
		{"HEADING", docFusionScopeHeading},
		{"  heading  ", docFusionScopeHeading},
		{"bogus", docFusionScopeAll},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := parseDocFusionScope(tc.in); got != tc.want {
				t.Fatalf("parseDocFusionScope(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildDocOpRanking_RankOrderAndFirstSeenWins(t *testing.T) {
	sectionIDs := []string{"s1", "s2", "s3"}
	refs := map[string][]domain.DocRef{
		"s1": {{Kind: domain.RefOperation, Value: "op.a"}, {Kind: domain.RefSchema, Value: "SchemaX"}},
		"s2": {{Kind: domain.RefOperation, Value: "op.b"}, {Kind: domain.RefOperation, Value: "op.a"}},
		"s3": {{Kind: domain.RefOperation, Value: "op.c"}},
	}

	got := buildDocOpRanking(sectionIDs, refs, nil)

	wantIDs := []string{"op.a", "op.b", "op.c"}
	if !reflect.DeepEqual(got.opIDs, wantIDs) {
		t.Fatalf("opIDs = %v, want %v", got.opIDs, wantIDs)
	}
	if got.rawScore["op.a"] != 1.0 {
		t.Errorf("op.a rawScore = %v, want 1.0 (rank 0, and s2's later mention must not overwrite it)", got.rawScore["op.a"])
	}
	if got.rawScore["op.b"] != 0.5 {
		t.Errorf("op.b rawScore = %v, want 0.5 (rank 1)", got.rawScore["op.b"])
	}
	if got.rawScore["op.c"] != 1.0/3.0 {
		t.Errorf("op.c rawScore = %v, want 1/3 (rank 2)", got.rawScore["op.c"])
	}
	// SchemaX is a RefSchema, not a RefOperation: never counted.
	if _, ok := got.rawScore["SchemaX"]; ok {
		t.Errorf("a non-operation ref must not appear in rawScore")
	}
}

func TestBuildDocOpRanking_AllowedRestrictsVotes(t *testing.T) {
	sectionIDs := []string{"s1", "s2"}
	refs := map[string][]domain.DocRef{
		"s1": {{Kind: domain.RefOperation, Value: "op.a"}, {Kind: domain.RefOperation, Value: "op.b"}},
		"s2": {{Kind: domain.RefOperation, Value: "op.c"}},
	}
	// s1 may only vote for op.a (op.b named elsewhere in its body, not its
	// heading/first paragraph, say); s2's allowed set omits op.c entirely.
	allowed := map[string]map[string]bool{
		"s1": {"op.a": true},
		"s2": {},
	}

	got := buildDocOpRanking(sectionIDs, refs, allowed)

	wantIDs := []string{"op.a"}
	if !reflect.DeepEqual(got.opIDs, wantIDs) {
		t.Fatalf("opIDs = %v, want %v (op.b and op.c must be excluded by allowed)", got.opIDs, wantIDs)
	}
}

func TestBuildDocOpRanking_EmptyInputs(t *testing.T) {
	got := buildDocOpRanking(nil, nil, nil)
	if len(got.opIDs) != 0 || len(got.rawScore) != 0 {
		t.Fatalf("expected empty ranking for empty inputs, got %+v", got)
	}
}

func TestWeightedFuseScores_UnionAndWeighting(t *testing.T) {
	base := map[string]float64{"a": 1.0, "b": 0.5}
	doc := map[string]float64{"b": 1.0, "c": 0.3}

	got := weightedFuseScores(base, doc, 0.6)

	want := map[string]float64{
		"a": 0.6*1.0 + 0,       // doc-only side contributes 0 when id absent from doc
		"b": 0.6*0.5 + 0.4*1.0, // present on both sides
		"c": 0.6*0 + 0.4*0.3,   // base-only side contributes 0 when id absent from base
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("weightedFuseScores[%q] = %v, want %v", id, got[id], w)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("weightedFuseScores produced %d ids, want %d (%v)", len(got), len(want), got)
	}
}

func TestWeightedFuseScores_ExtremeWeightsDegenerateToOneSide(t *testing.T) {
	base := map[string]float64{"a": 0.4}
	doc := map[string]float64{"a": 0.9, "b": 0.2}

	allLex := weightedFuseScores(base, doc, 1.0)
	if allLex["a"] != 0.4 {
		t.Errorf("lexWeight=1: a = %v, want 0.4 (doc side fully zeroed)", allLex["a"])
	}
	if allLex["b"] != 0 {
		t.Errorf("lexWeight=1: b = %v, want 0", allLex["b"])
	}

	allDoc := weightedFuseScores(base, doc, 0.0)
	if allDoc["a"] != 0.9 {
		t.Errorf("lexWeight=0: a = %v, want 0.9 (base side fully zeroed)", allDoc["a"])
	}
	if allDoc["b"] != 0.2 {
		t.Errorf("lexWeight=0: b = %v, want 0.2", allDoc["b"])
	}
}

func TestWeightedFuseScores_ClampsOutOfRangeWeight(t *testing.T) {
	base := map[string]float64{"a": 1.0}
	doc := map[string]float64{"a": 1.0}

	tooHigh := weightedFuseScores(base, doc, 1.4)
	if tooHigh["a"] != 1.0 {
		t.Errorf("weight>1 should clamp to 1: got %v", tooHigh["a"])
	}
	tooLow := weightedFuseScores(base, doc, -0.4)
	if tooLow["a"] != 1.0 {
		t.Errorf("weight<0 should clamp to 0: got %v", tooLow["a"])
	}
}

func TestDocFusionContributed(t *testing.T) {
	docSet := map[string]bool{"op.a": true, "op.b": true}
	preFusionRank := map[string]int{"op.a": 5, "op.c": 0}
	fusedRank := map[string]int{"op.a": 2, "op.b": 0, "op.c": 1}

	cases := []struct {
		name string
		id   string
		want bool
	}{
		{"moved up: was rank 5, now rank 2", "op.a", true},
		{"entered: no pre-fusion rank at all, but in docSet", "op.b", true},
		{"not a docs candidate at all: never counts even though it moved", "op.c", false},
		{"absent from fused entirely", "op.d", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := docFusionContributed(tc.id, docSet, preFusionRank, fusedRank); got != tc.want {
				t.Errorf("docFusionContributed(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestDocFusionContributed_NotMovedStaysFalse(t *testing.T) {
	docSet := map[string]bool{"op.a": true}
	preFusionRank := map[string]int{"op.a": 0}
	fusedRank := map[string]int{"op.a": 0}
	if docFusionContributed("op.a", docSet, preFusionRank, fusedRank) {
		t.Error("rank unchanged should not count as contributed")
	}
}

func TestAppendMatchedOnUnique(t *testing.T) {
	out := appendMatchedOnUnique([]string{"summary"}, "docs")
	if !reflect.DeepEqual(out, []string{"summary", "docs"}) {
		t.Fatalf("got %v", out)
	}
	out2 := appendMatchedOnUnique(out, "docs")
	if !reflect.DeepEqual(out2, out) {
		t.Fatalf("appending an existing label should be a no-op: got %v", out2)
	}
}

func TestIdsOfAndScoreMap(t *testing.T) {
	list := []scoredID{{id: "a", score: 1}, {id: "b", score: 2}}
	if got := idsOf(list); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("idsOf = %v", got)
	}
	sm := scoreMap(list)
	if sm["a"] != 1 || sm["b"] != 2 || len(sm) != 2 {
		t.Fatalf("scoreMap = %v", sm)
	}
}

func TestRankIndex(t *testing.T) {
	list := []scoredID{{id: "x", score: 9}, {id: "y", score: 3}}
	ri := rankIndex(list)
	if ri["x"] != 0 || ri["y"] != 1 {
		t.Fatalf("rankIndex = %v", ri)
	}
}

func TestFirstParagraph(t *testing.T) {
	cases := []struct{ in, want string }{
		{"one line only", "one line only"},
		{"para one.\n\npara two.", "para one."},
		{"  padded  \n\nmore", "padded"},
		{"", ""},
		{"line1\nline2\n\nline3", "line1\nline2"},
	}
	for _, tc := range cases {
		if got := firstParagraph(tc.in); got != tc.want {
			t.Errorf("firstParagraph(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOperationNamedIn(t *testing.T) {
	meta := opMeta{rawOpID: "allocate"}
	op := domain.Operation{HTTP: &domain.HTTPBinding{Path: "/v1/allocations"}}

	if !operationNamedIn("see the allocate endpoint below", "allocation-service.allocate", meta, op) {
		t.Error("expected a raw_op_id substring match")
	}
	if !operationNamedIn("POST /v1/allocations creates one", "allocation-service.allocate", meta, op) {
		t.Error("expected an HTTP path substring match")
	}
	if operationNamedIn("nothing relevant here", "allocation-service.allocate", meta, op) {
		t.Error("expected no match")
	}
	if operationNamedIn("", "allocation-service.allocate", meta, op) {
		t.Error("empty text must never match")
	}
}
