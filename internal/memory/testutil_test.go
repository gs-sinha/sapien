package memory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/memory"
	"github.com/gs-sinha/sapien/internal/store"
)

// fakeResolver is a table-driven, in-memory memory.Resolver used across this
// package's tests instead of internal/catalog (which internal/memory must
// not import; see the task's HARD RULES).
type fakeResolver struct {
	ops    map[string]memory.OperationInfo
	flows  map[string][]string // operation ID -> flow IDs that use it
	fields map[string]bool     // "<operationID>#<fieldPath>" -> exists
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{
		ops:    map[string]memory.OperationInfo{},
		flows:  map[string][]string{},
		fields: map[string]bool{},
	}
}

func (f *fakeResolver) withOp(id string, info memory.OperationInfo) *fakeResolver {
	f.ops[id] = info
	return f
}

func (f *fakeResolver) withFlow(operationID string, flowIDs ...string) *fakeResolver {
	f.flows[operationID] = append(f.flows[operationID], flowIDs...)
	return f
}

func (f *fakeResolver) withField(operationID, fieldPath string) *fakeResolver {
	f.fields[operationID+"#"+fieldPath] = true
	return f
}

func (f *fakeResolver) Operation(_ context.Context, id string) (memory.OperationInfo, bool) {
	info, ok := f.ops[id]
	return info, ok
}

func (f *fakeResolver) FlowsUsing(_ context.Context, operationID string) []string {
	return f.flows[operationID]
}

func (f *fakeResolver) FieldExists(_ context.Context, operationID, fieldPath string) bool {
	return f.fields[operationID+"#"+fieldPath]
}

// openTestDB opens an in-memory SQLite database (migrated), closing it on
// test cleanup.
func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
