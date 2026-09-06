package enginetest

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
)

// hashOf returns a short, deterministic content hash suitable for
// domain.Operation.Hash, domain.NamedSchema.Hash, and domain.FlowSummary.Hash
// fields in this fake.
func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// stemOf returns the flow ID implied by a file path, stripping
// domain.FlowFileSuffix (".flow.yaml") if present, else any extension.
func stemOf(path string) string {
	base := filepath.Base(path)
	if strings.HasSuffix(base, domain.FlowFileSuffix) {
		return strings.TrimSuffix(base, domain.FlowFileSuffix)
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}
