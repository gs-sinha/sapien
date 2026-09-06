package spec_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/growsimplee/sapien/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSchemasSyncedWithSpecDir guards against the embedded copy under
// internal/spec/schemas/ drifting from the canonical, human-facing schemas
// in spec/ (go:embed cannot reach outside this package's directory, so the
// two must be kept in sync by hand -- see embed.go).
func TestSchemasSyncedWithSpecDir(t *testing.T) {
	kinds := []spec.Kind{spec.Workspace, spec.Service, spec.Environment, spec.Flow, spec.Memory, spec.Example}
	for _, k := range kinds {
		k := k
		t.Run(string(k), func(t *testing.T) {
			canonical, err := os.ReadFile(filepath.Join("..", "..", "spec", string(k)+".schema.json"))
			require.NoError(t, err)
			assert.Equal(t, string(canonical), string(spec.Schema(k)),
				"internal/spec/schemas/%s.schema.json has drifted from spec/%s.schema.json", k, k)
		})
	}
}
