package store

import (
	"crypto/rand"

	"github.com/oklog/ulid/v2"
)

// entropySource is a process-wide monotonic entropy source for ULID generation.
// ulid.Monotonic guarantees strictly increasing IDs for calls within the same
// millisecond; LockedMonotonicReader wraps it with a mutex, which per the
// ulid.New docs ("safety for concurrent use is only dependent on the safety of
// the entropy source") is sufficient to make NewID safe for concurrent use.
var entropySource = &ulid.LockedMonotonicReader{
	MonotonicReader: ulid.Monotonic(rand.Reader, 0),
}

// NewID returns a new lexicographically sortable identifier of the form
// "<prefix>_<ULID>", e.g. "mem_01J8Z3K2N4Q5R6S7T8U9V0W1X2". It is safe for
// concurrent use, and IDs generated within the same process are monotonically
// increasing for calls within the same millisecond.
func NewID(prefix string) string {
	id := ulid.MustNew(ulid.Now(), entropySource)
	return prefix + "_" + id.String()
}
