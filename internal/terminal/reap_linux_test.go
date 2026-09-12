//go:build linux

package terminal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseProcStatReadsPastACommContainingSpacesAndParentheses(t *testing.T) {
	// The second field is the executable's name in parentheses, unquoted
	// and unescaped, so it can contain anything -- including the spaces
	// and brackets that would misalign a naive split of the whole line.
	line := "4242 (sleep 30) (x)) S 4200 4241 4100 34816 4241 4194304 123 0 0 0\n"

	e, ok := parseProcStat(4242, line)

	require.True(t, ok)
	assert.Equal(t, procEntry{pid: 4242, ppid: 4200, pgid: 4241, sid: 4100}, e)
}

func TestParseProcStatRejectsALineItCannotRead(t *testing.T) {
	// A truncated read, or /proc handing us something unexpected: better
	// to drop the row than to invent a pid to kill.
	for _, line := range []string{"", "no parenthesis here", "1 (init) S 0 1"} {
		_, ok := parseProcStat(1, line)
		assert.False(t, ok, "should not have parsed %q", line)
	}
}
