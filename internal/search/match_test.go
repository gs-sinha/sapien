package search_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/search"
)

func TestBuildMatch(t *testing.T) {
	cases := []struct {
		name   string
		tokens []string
		mode   string
		want   string
	}{
		{"and short and long", []string{"v1", "riders"}, "and", `"v1" AND "riders"*`},
		{"or joins with OR", []string{"foo", "bar"}, "or", `"foo"* OR "bar"*`},
		{"unknown mode defaults to and", []string{"foo", "bar"}, "weird", `"foo"* AND "bar"*`},
		{"case-insensitive or", []string{"foo"}, "OR", `"foo"*`},
		{"exactly three chars gets a star", []string{"abc"}, "and", `"abc"*`},
		{"two chars gets no star", []string{"ab"}, "and", `"ab"`},
		{"embedded quote is doubled", []string{`co"mma`}, "and", `"co""mma"*`},
		{"empty tokens", nil, "and", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, search.BuildMatch(tc.tokens, tc.mode))
		})
	}
}

// TestBuildMatch_SafeAgainstFTSSyntaxErrors verifies that quoting really does
// neutralize FTS5 query syntax characters (parens, colons, hyphens) by
// running the built MATCH string against a real operations_fts table.
func TestBuildMatch_SafeAgainstFTSSyntaxErrors(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())

	weirdTokens := [][]string{
		{"orders", "(draft)"},
		{"foo:bar"},
		{"a-b-c"},
		{`quo"te`},
		{"NOT", "AND", "OR"}, // FTS5 keywords as literal tokens
	}
	run := func(match string) error {
		rows, err := db.SQL().QueryContext(context.Background(),
			`SELECT id FROM operations_fts WHERE operations_fts MATCH ?`, match)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
		}
		return rows.Err()
	}

	for _, tokens := range weirdTokens {
		match := search.BuildMatch(tokens, "and")
		require.NoErrorf(t, run(match), "match string %q should not be a syntax error", match)

		match = search.BuildMatch(tokens, "or")
		require.NoErrorf(t, run(match), "match string %q should not be a syntax error", match)
	}
}
