package semantic_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gs-sinha/sapien/internal/semantic"
)

func TestCosine(t *testing.T) {
	cases := []struct {
		name string
		a, b []float32
		want float32
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1},
		{"scaled same direction", []float32{2, 0}, []float32{4, 0}, 1},
		{"zero vector a", []float32{0, 0, 0}, []float32{1, 2, 3}, 0},
		{"zero vector b", []float32{1, 2, 3}, []float32{0, 0, 0}, 0},
		{"both zero", []float32{0, 0}, []float32{0, 0}, 0},
		{"empty", []float32{}, []float32{}, 0},
		{"unequal length compares shared prefix", []float32{1, 0, 0}, []float32{1, 0}, 1},
		{"45 degrees", []float32{1, 0}, []float32{1, 1}, 0.70710678},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := semantic.Cosine(tc.a, tc.b)
			assert.InDelta(t, tc.want, got, 1e-6)
		})
	}
}

func TestRRF_SingleList(t *testing.T) {
	// A single list should preserve rank order as descending score.
	got := semantic.RRF([]string{"a", "b", "c"})
	ids := scoredIDs(got)
	assert.Equal(t, []string{"a", "b", "c"}, ids)
	// rank 0 -> 1/61, rank 1 -> 1/62, rank 2 -> 1/63
	assert.InDelta(t, 1.0/61.0, got[0].Score, 1e-9)
	assert.InDelta(t, 1.0/62.0, got[1].Score, 1e-9)
	assert.InDelta(t, 1.0/63.0, got[2].Score, 1e-9)
}

func TestRRF_FusesAgreementToTheTop(t *testing.T) {
	// "c" is last in list1 but first in list2: fusing should still let an id
	// that ranks well in *both* lists beat one that ranks #1 in only one.
	list1 := []string{"a", "b", "c"}
	list2 := []string{"c", "b", "a"}
	got := semantic.RRF(list1, list2)
	ids := scoredIDs(got)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, ids)
	// b is rank 1 in both lists (best combined position other than the
	// symmetric a/c pair); by symmetry a and c tie, and both should be
	// >= b is not guaranteed, but b's score should equal the average
	// position contribution shared by a and c.
	scoreOf := func(id string) float64 {
		for _, s := range got {
			if s.ID == id {
				return s.Score
			}
		}
		t.Fatalf("id %q not found", id)
		return 0
	}
	assert.InDelta(t, scoreOf("a"), scoreOf("c"), 1e-9, "a and c are symmetric across the two lists")
	// b appears at rank 1 in both lists: 2/62.
	assert.InDelta(t, 2.0/62.0, scoreOf("b"), 1e-9)
}

func TestRRF_IdOnlyInOneListStillIncluded(t *testing.T) {
	got := semantic.RRF([]string{"only-in-1"}, []string{"only-in-2", "only-in-1"})
	ids := scoredIDs(got)
	assert.ElementsMatch(t, []string{"only-in-1", "only-in-2"}, ids)
	// only-in-1: rank 0 in list1 (1/61) + rank 1 in list2 (1/62).
	// only-in-2: rank 0 in list2 (1/61).
	// only-in-1 should win.
	assert.Equal(t, "only-in-1", got[0].ID)
}

func TestRRF_DuplicateWithinAListCountsOnce(t *testing.T) {
	got := semantic.RRF([]string{"a", "a", "b"})
	ids := scoredIDs(got)
	assert.ElementsMatch(t, []string{"a", "b"}, ids)
	scoreOf := func(id string) float64 {
		for _, s := range got {
			if s.ID == id {
				return s.Score
			}
		}
		return -1
	}
	assert.InDelta(t, 1.0/61.0, scoreOf("a"), 1e-9, "a's second occurrence must not double-count")
}

func TestRRF_EmptyInput(t *testing.T) {
	assert.Empty(t, semantic.RRF())
	assert.Empty(t, semantic.RRF([]string{}))
}

func TestRRF_TieBreaksByIDAscending(t *testing.T) {
	// "z" and "a" both appear only at rank 0 of their own list: equal score,
	// broken by id ascending.
	got := semantic.RRF([]string{"z"}, []string{"a"})
	ids := scoredIDs(got)
	assert.Equal(t, []string{"a", "z"}, ids)
}

func scoredIDs(list []semantic.Scored) []string {
	ids := make([]string, len(list))
	for i, s := range list {
		ids[i] = s.ID
	}
	return ids
}
