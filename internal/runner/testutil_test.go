package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/env"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/expr"
	"github.com/growsimplee/sapien/internal/ingest/openapi"
)

// newTestEvaluator returns a fresh expr.Evaluator for tests exercising
// runner helpers that take one directly (e.g. resolveDefault).
func newTestEvaluator() *expr.Evaluator { return expr.New() }

// fixturesRoot returns the absolute path to fixtures/logistics, the same way
// internal/registry's tests locate it.
func fixturesRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "..", "..", "fixtures", "logistics")
}

// mapOperations is a minimal Operations backed by a map, built from the
// fixture contracts via openapi.IngestFile.
type mapOperations struct {
	ops map[string]*domain.Operation
}

func (m mapOperations) Operation(_ context.Context, id string) (*domain.Operation, error) {
	op, ok := m.ops[id]
	if !ok {
		return nil, errs.New(errs.OperationNotFound, "operation %q not found", id).WithDetail("id", id)
	}
	return op, nil
}

// buildTestOperations ingests the three logistics fixture contracts and
// returns an Operations resolving every operation ID they declare (e.g.
// "order-service.createOrder").
func buildTestOperations(t *testing.T) Operations {
	t.Helper()
	root := fixturesRoot(t)
	ops := map[string]*domain.Operation{}
	for _, svc := range []string{"order-service", "allocation-service", "rider-service"} {
		path := filepath.Join(root, svc, "api", "openapi.yaml")
		result, err := openapi.IngestFile(path, openapi.Options{ServiceID: svc})
		require.NoError(t, err, "ingesting %s", path)
		for i := range result.Operations {
			op := result.Operations[i]
			ops[op.ID] = &op
		}
	}
	return mapOperations{ops: ops}
}

// buildTestEnv builds an *env.Resolved wiring the three services to the
// given base URLs.
func buildTestEnv(name string, production bool, orderURL, allocURL, riderURL string) *env.Resolved {
	e := domain.Environment{
		Version:    1,
		Name:       name,
		Production: production,
		Services: map[string]domain.ServiceEnv{
			"order-service":      {BaseURL: orderURL},
			"allocation-service": {BaseURL: allocURL},
			"rider-service":      {BaseURL: riderURL},
		},
	}
	services := []domain.Service{{Name: "order-service"}, {Name: "allocation-service"}, {Name: "rider-service"}}
	return env.Resolve(e, services, nil)
}

// instantSleep never actually waits; it's used with an advancing Now so poll
// loops in tests complete instantly instead of taking wall-clock time.
func instantSleep(ctx context.Context, _ time.Duration) error {
	return ctx.Err()
}
