package openapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/gs-sinha/sapien/internal/domain"
)

// hashJSON returns the hex-encoded sha256 of v's canonical JSON encoding.
// encoding/json marshals map keys in sorted order, so this is stable regardless of
// map iteration order; struct fields marshal in a fixed declaration order.
func hashJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// Marshaling a normalized domain value should never fail; fall back to a
		// stable, if uninformative, hash rather than panicking.
		b = []byte(err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// hashOperation computes Operation.Hash: sha256 of the operation's canonical JSON
// with Hash and Source zeroed, so the hash reflects content only.
func hashOperation(op domain.Operation) string {
	op.Hash = ""
	op.Source = domain.SourceLoc{}
	return hashJSON(op)
}

func hashSchema(s *domain.Schema) string {
	return hashJSON(s)
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
