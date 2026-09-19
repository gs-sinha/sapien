package expr

import (
	"bufio"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extractCelExamples pulls every line out of ```cel fenced blocks in md.
func extractCelExamples(t *testing.T, md string) []string {
	t.Helper()
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(md))
	inBlock := false
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "```cel":
			inBlock = true
		case inBlock && trimmed == "```":
			inBlock = false
		case inBlock && trimmed != "":
			lines = append(lines, trimmed)
		}
	}
	require.NoError(t, sc.Err())
	return lines
}

func referenceSampleScope() Scope {
	return Scope{
		Inputs: map[string]any{"city": "SF"},
		Env:    map[string]string{"TOKEN": "tok123"},
		Steps: map[string]StepValue{
			"create": {
				Status: 201,
				Body:   map[string]any{"orderId": "ord_1"},
				Out:    map[string]any{"orderId": "ord_1"},
			},
			"each": {
				IsBlock: true,
				Count:   3,
				Iterations: []map[string]StepValue{
					{"create": {Status: 201, Out: map[string]any{"orderId": "ord_1"}}},
					{"create": {Status: 201, Out: map[string]any{"orderId": "ord_2"}}},
					{"create": {Status: 201, Out: map[string]any{"orderId": "ord_3"}}},
				},
			},
		},
		Iter: &IterValue{Item: "ord_1", Index: 0},
		Current: &StepValue{
			Status:    200,
			Headers:   map[string]string{"content-type": "application/json"},
			LatencyMs: 850,
			Body: map[string]any{
				"riderId": "r1",
				"online":  true,
				"count":   2,
				"items":   []any{"a", "b"},
				"name":    "QCOM rider",
				"tags":    []any{"x", "y"},
			},
		},
	}
}

// TestReference_ExamplesEvaluate parses every fenced ```cel example out of
// Reference() and confirms it evaluates (as a template, if it contains
// `${`, otherwise as bare CEL) against a representative sample scope. This
// keeps the reference doc honest as the implementation evolves.
func TestReference_ExamplesEvaluate(t *testing.T) {
	md := Reference()
	assert.LessOrEqual(t, strings.Count(md, "\n"), 120, "Reference() should stay concise")

	examples := extractCelExamples(t, md)
	require.NotEmpty(t, examples)

	e := New()
	s := referenceSampleScope()
	for _, line := range examples {
		if strings.Contains(line, "${") {
			_, _, err := e.Interpolate(line, s)
			assert.NoError(t, err, "interpolate %q", line)
			continue
		}
		_, err := e.Eval(line, s)
		assert.NoError(t, err, "eval %q", line)
	}
}
