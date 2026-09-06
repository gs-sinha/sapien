package remote_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
	"github.com/growsimplee/sapien/internal/engine/enginetest"
	"github.com/growsimplee/sapien/internal/engine/remote"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/server"
)

const testToken = "test-token"

// harness wires a seeded enginetest.Fake to a real internal/server Server
// (over httptest) and a real remote.Remote client talking to it -- so
// every test in this file exercises the full JSON-over-HTTP round trip,
// not just the two packages' Go types.
type harness struct {
	fake *enginetest.Fake
	rc   *remote.Remote
	ts   *httptest.Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	ws := &domain.Workspace{Version: 1, Name: "logistics", Dir: t.TempDir()}
	fake := enginetest.New(ws)
	enginetest.Seed(fake)

	srv := server.New(server.Options{
		Engine:  fake,
		Token:   testToken,
		Version: "1.2.3",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	rc, err := remote.New(ts.URL, testToken)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Close() })

	return &harness{fake: fake, rc: rc, ts: ts}
}

// normalize JSON-round-trips v (marshal then unmarshal into `any`) so
// comparisons via assertSame don't trip on nil-vs-empty slices or time
// zone/precision differences between two independently constructed Go
// values that represent the same JSON.
func normalize(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

func assertSame(t *testing.T, want, got any) {
	t.Helper()
	assert.Equal(t, normalize(t, want), normalize(t, got))
}

func TestNewFetchesAndCachesWorkspace(t *testing.T) {
	h := newHarness(t)
	assertSame(t, h.fake.Workspace(), h.rc.Workspace())
}

func TestNewWithWrongTokenFails(t *testing.T) {
	h := newHarness(t)
	_, err := remote.New(h.ts.URL, "wrong-token")
	require.Error(t, err)
	assert.Equal(t, errs.PermissionDenied, errs.CodeOf(err))
}

func TestWithHTTPClient(t *testing.T) {
	ws := &domain.Workspace{Version: 1, Name: "logistics", Dir: t.TempDir()}
	fake := enginetest.New(ws)
	enginetest.Seed(fake)

	srv := server.New(server.Options{
		Engine:  fake,
		Token:   testToken,
		Version: "1.2.3",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	custom := &http.Client{Timeout: 5 * time.Second}
	rc, err := remote.New(ts.URL, testToken, remote.WithHTTPClient(custom))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Close() })

	got, err := rc.Services().List(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, got)
}

func TestServicesRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	t.Run("List", func(t *testing.T) {
		got, err := h.rc.Services().List(ctx)
		require.NoError(t, err)
		want, err := h.fake.Services().List(ctx)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("Get", func(t *testing.T) {
		got, err := h.rc.Services().Get(ctx, "order-service")
		require.NoError(t, err)
		want, err := h.fake.Services().Get(ctx, "order-service")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("GetNotFound", func(t *testing.T) {
		_, err := h.rc.Services().Get(ctx, "no-such-service")
		require.Error(t, err)
		assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
	})

	t.Run("Add", func(t *testing.T) {
		src := domain.Source{Kind: domain.SourceLocal, Path: "services/new-service"}
		got, err := h.rc.Services().Add(ctx, "new-service", src)
		require.NoError(t, err)
		want, err := h.fake.Services().Get(ctx, "new-service")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("Remove", func(t *testing.T) {
		require.NoError(t, h.rc.Services().Remove(ctx, "rider-service"))
		_, err := h.fake.Services().Get(ctx, "rider-service")
		require.Error(t, err)
		assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
	})

	t.Run("SyncAll", func(t *testing.T) {
		got, err := h.rc.Services().Sync(ctx, "")
		require.NoError(t, err)
		want, err := h.fake.Services().Sync(ctx, "")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("SyncOne", func(t *testing.T) {
		got, err := h.rc.Services().Sync(ctx, "order-service")
		require.NoError(t, err)
		want, err := h.fake.Services().Sync(ctx, "order-service")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("Reindex", func(t *testing.T) {
		require.NoError(t, h.rc.Services().Reindex(ctx))
	})
}

func TestCatalogRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	t.Run("GetOperation", func(t *testing.T) {
		got, err := h.rc.Catalog().GetOperation(ctx, "order-service.createOrder")
		require.NoError(t, err)
		want, err := h.fake.Catalog().GetOperation(ctx, "order-service.createOrder")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("ResolveOperationByMethodAndPath", func(t *testing.T) {
		got, err := h.rc.Catalog().ResolveOperation(ctx, "GET /v1/riders/{riderId}")
		require.NoError(t, err)
		want, err := h.fake.Catalog().ResolveOperation(ctx, "GET /v1/riders/{riderId}")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("ResolveOperationByBareID", func(t *testing.T) {
		got, err := h.rc.Catalog().ResolveOperation(ctx, "createOrder")
		require.NoError(t, err)
		want, err := h.fake.Catalog().ResolveOperation(ctx, "createOrder")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("ListOperations", func(t *testing.T) {
		got, err := h.rc.Catalog().ListOperations(ctx, "order-service")
		require.NoError(t, err)
		want, err := h.fake.Catalog().ListOperations(ctx, "order-service")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("ListOperationsNoServiceFilter", func(t *testing.T) {
		got, err := h.rc.Catalog().ListOperations(ctx, "")
		require.NoError(t, err)
		want, err := h.fake.Catalog().ListOperations(ctx, "")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("Fields", func(t *testing.T) {
		got, err := h.rc.Catalog().Fields(ctx, "order-service.createOrder")
		require.NoError(t, err)
		want, err := h.fake.Catalog().Fields(ctx, "order-service.createOrder")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("FieldsNotFound", func(t *testing.T) {
		_, err := h.rc.Catalog().Fields(ctx, "nope.doesNotExist")
		require.Error(t, err)
		assert.Equal(t, errs.OperationNotFound, errs.CodeOf(err))
	})

	t.Run("GetSchema", func(t *testing.T) {
		got, err := h.rc.Catalog().GetSchema(ctx, "order-service", "Order")
		require.NoError(t, err)
		want, err := h.fake.Catalog().GetSchema(ctx, "order-service", "Order")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("GetSchemaNotFound", func(t *testing.T) {
		_, err := h.rc.Catalog().GetSchema(ctx, "order-service", "NoSuchSchema")
		require.Error(t, err)
		assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
	})

	t.Run("ListDocs", func(t *testing.T) {
		got, err := h.rc.Catalog().ListDocs(ctx, "")
		require.NoError(t, err)
		want, err := h.fake.Catalog().ListDocs(ctx, "")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("ListDocsWithServiceFilter", func(t *testing.T) {
		got, err := h.rc.Catalog().ListDocs(ctx, "rider-service")
		require.NoError(t, err)
		want, err := h.fake.Catalog().ListDocs(ctx, "rider-service")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("GetDoc", func(t *testing.T) {
		got, err := h.rc.Catalog().GetDoc(ctx, "order-service", "docs/orders.md")
		require.NoError(t, err)
		want, err := h.fake.Catalog().GetDoc(ctx, "order-service", "docs/orders.md")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("GetDocNotFound", func(t *testing.T) {
		_, err := h.rc.Catalog().GetDoc(ctx, "order-service", "docs/no-such-doc.md")
		require.Error(t, err)
		assert.Equal(t, errs.DocNotFound, errs.CodeOf(err))
	})
}

func TestSearchRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	opts := domain.SearchOptions{Limit: 10}

	t.Run("Operations", func(t *testing.T) {
		got, err := h.rc.Search().Operations(ctx, "order", opts)
		require.NoError(t, err)
		want, err := h.fake.Search().Operations(ctx, "order", opts)
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.NotEmpty(t, got)
	})

	t.Run("OperationsWithAllOptions", func(t *testing.T) {
		full := domain.SearchOptions{Service: "order-service", Method: "POST", Limit: 5, IncludeDeprecated: true}
		got, err := h.rc.Search().Operations(ctx, "order", full)
		require.NoError(t, err)
		want, err := h.fake.Search().Operations(ctx, "order", full)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("Docs", func(t *testing.T) {
		got, err := h.rc.Search().Docs(ctx, "overview", opts)
		require.NoError(t, err)
		want, err := h.fake.Search().Docs(ctx, "overview", opts)
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.NotEmpty(t, got)
	})

	t.Run("DocsWithServiceFilter", func(t *testing.T) {
		full := domain.SearchOptions{Service: "order-service", Limit: 5}
		got, err := h.rc.Search().Docs(ctx, "overview", full)
		require.NoError(t, err)
		want, err := h.fake.Search().Docs(ctx, "overview", full)
		require.NoError(t, err)
		assertSame(t, want, got)
	})
}

const tempFlowYAML = "version: 1\n" +
	"id: temp-flow\n" +
	"name: Temp\n" +
	"steps:\n" +
	"  - id: s1\n" +
	"    call: order-service.getOrder\n" +
	"    params:\n" +
	"      path:\n" +
	"        orderId: order_1\n"

func TestFlowsRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	t.Run("List", func(t *testing.T) {
		got, err := h.rc.Flows().List(ctx, "")
		require.NoError(t, err)
		want, err := h.fake.Flows().List(ctx, "")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("ListWithQuery", func(t *testing.T) {
		got, err := h.rc.Flows().List(ctx, "order")
		require.NoError(t, err)
		want, err := h.fake.Flows().List(ctx, "order")
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.NotEmpty(t, got)
	})

	t.Run("Get", func(t *testing.T) {
		got, err := h.rc.Flows().Get(ctx, "create-order-flow")
		require.NoError(t, err)
		want, err := h.fake.Flows().Get(ctx, "create-order-flow")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("GetNotFound", func(t *testing.T) {
		_, err := h.rc.Flows().Get(ctx, "no-such-flow")
		require.Error(t, err)
		assert.Equal(t, errs.FlowNotFound, errs.CodeOf(err))
	})

	t.Run("Parse", func(t *testing.T) {
		got, err := h.rc.Flows().Parse(ctx, tempFlowYAML)
		require.NoError(t, err)
		want, err := h.fake.Flows().Parse(ctx, tempFlowYAML)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("ParseInvalidYAML", func(t *testing.T) {
		_, err := h.rc.Flows().Parse(ctx, "not: [valid")
		require.Error(t, err)
		assert.Equal(t, errs.FlowInvalid, errs.CodeOf(err))
	})

	t.Run("Validate", func(t *testing.T) {
		got, err := h.rc.Flows().Validate(ctx, tempFlowYAML)
		require.NoError(t, err)
		want, err := h.fake.Flows().Validate(ctx, tempFlowYAML)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("Reference", func(t *testing.T) {
		got, err := h.rc.Flows().Reference(ctx, "flow")
		require.NoError(t, err)
		want, err := h.fake.Flows().Reference(ctx, "flow")
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("ReferenceUnknownTopic", func(t *testing.T) {
		_, err := h.rc.Flows().Reference(ctx, "nope")
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})

	t.Run("Create", func(t *testing.T) {
		got, err := h.rc.Flows().Create(ctx, tempFlowYAML, "flows/temp-flow.flow.yaml")
		require.NoError(t, err)
		want, err := h.fake.Flows().Get(ctx, "temp-flow")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("Update", func(t *testing.T) {
		updated := "version: 1\nid: temp-flow\nname: Temp Updated\nsteps:\n  - id: s1\n    call: order-service.getOrder\n    params:\n      path:\n        orderId: order_2\n"
		got, err := h.rc.Flows().Update(ctx, "temp-flow", updated)
		require.NoError(t, err)
		want, err := h.fake.Flows().Get(ctx, "temp-flow")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("UpdateNotFound", func(t *testing.T) {
		_, err := h.rc.Flows().Update(ctx, "no-such-flow", tempFlowYAML)
		require.Error(t, err)
		assert.Equal(t, errs.FlowNotFound, errs.CodeOf(err))
	})

	t.Run("Delete", func(t *testing.T) {
		require.NoError(t, h.rc.Flows().Delete(ctx, "temp-flow"))
		_, err := h.fake.Flows().Get(ctx, "temp-flow")
		require.Error(t, err)
		assert.Equal(t, errs.FlowNotFound, errs.CodeOf(err))
	})

	t.Run("DeleteNotFound", func(t *testing.T) {
		err := h.rc.Flows().Delete(ctx, "temp-flow")
		require.Error(t, err)
		assert.Equal(t, errs.FlowNotFound, errs.CodeOf(err))
	})
}

func TestRunnerAndRunsRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	t.Run("RunFlow", func(t *testing.T) {
		opts := engine.RunOptions{Environment: "local", Inputs: map[string]any{"customerId": "cust_9"}, Trigger: "cli"}
		run, err := h.rc.Runner().RunFlow(ctx, "create-order-flow", opts)
		require.NoError(t, err)
		want, err := h.fake.Runs().Get(ctx, run.ID)
		require.NoError(t, err)
		assertSame(t, want, run)
	})

	t.Run("RunFlowUnknownFlow", func(t *testing.T) {
		_, err := h.rc.Runner().RunFlow(ctx, "does-not-exist", engine.RunOptions{})
		require.Error(t, err)
		assert.Equal(t, errs.FlowNotFound, errs.CodeOf(err))
	})

	t.Run("RunFlowSource", func(t *testing.T) {
		opts := engine.RunOptions{Environment: "local", Trigger: "cli"}
		run, err := h.rc.Runner().RunFlowSource(ctx, tempFlowYAML, opts)
		require.NoError(t, err)
		want, err := h.fake.Runs().Get(ctx, run.ID)
		require.NoError(t, err)
		assertSame(t, want, run)
	})

	t.Run("Call", func(t *testing.T) {
		req := engine.CallRequest{Operation: "order-service.getOrder", Params: map[string]any{"orderId": "order_5"}, Env: "local", Trigger: "cli"}
		run, err := h.rc.Runner().Call(ctx, req)
		require.NoError(t, err)
		want, err := h.fake.Runs().Get(ctx, run.ID)
		require.NoError(t, err)
		assertSame(t, want, run)
	})

	var cancelID string
	t.Run("SetUpForCancel", func(t *testing.T) {
		req := engine.CallRequest{Operation: "order-service.getOrder", Params: map[string]any{"orderId": "order_6"}}
		run, err := h.rc.Runner().Call(ctx, req)
		require.NoError(t, err)
		cancelID = run.ID
	})

	t.Run("Cancel", func(t *testing.T) {
		require.NoError(t, h.rc.Runner().Cancel(ctx, cancelID))
		want, err := h.fake.Runs().Get(ctx, cancelID)
		require.NoError(t, err)
		assert.Equal(t, domain.RunCancelled, want.Status)
	})

	t.Run("CancelNotFound", func(t *testing.T) {
		err := h.rc.Runner().Cancel(ctx, "run_does_not_exist")
		require.Error(t, err)
		assert.Equal(t, errs.RunNotFound, errs.CodeOf(err))
	})

	t.Run("RunsList", func(t *testing.T) {
		got, err := h.rc.Runs().List(ctx, domain.RunFilter{})
		require.NoError(t, err)
		want, err := h.fake.Runs().List(ctx, domain.RunFilter{})
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("RunsListWithFilter", func(t *testing.T) {
		filter := domain.RunFilter{FlowID: "create-order-flow", Status: domain.RunPassed, Operation: "order-service.createOrder", Limit: 5, Offset: 1}
		got, err := h.rc.Runs().List(ctx, filter)
		require.NoError(t, err)
		want, err := h.fake.Runs().List(ctx, filter)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("RunsGetNotFound", func(t *testing.T) {
		_, err := h.rc.Runs().Get(ctx, "run_does_not_exist")
		require.Error(t, err)
		assert.Equal(t, errs.RunNotFound, errs.CodeOf(err))
	})

	t.Run("PinNotFound", func(t *testing.T) {
		err := h.rc.Runs().Pin(ctx, "run_does_not_exist", true)
		require.Error(t, err)
		assert.Equal(t, errs.RunNotFound, errs.CodeOf(err))
	})

	t.Run("RunsGet", func(t *testing.T) {
		got, err := h.rc.Runs().Get(ctx, cancelID)
		require.NoError(t, err)
		want, err := h.fake.Runs().Get(ctx, cancelID)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("Pin", func(t *testing.T) {
		require.NoError(t, h.rc.Runs().Pin(ctx, cancelID, true))
		want, err := h.fake.Runs().Get(ctx, cancelID)
		require.NoError(t, err)
		assert.True(t, want.Pinned)
	})

	t.Run("Purge", func(t *testing.T) {
		removed, err := h.rc.Runs().Purge(ctx, 0)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, removed, 0)
		// cancelID was pinned just above, so Purge must never remove it.
		_, err = h.fake.Runs().Get(ctx, cancelID)
		require.NoError(t, err)
	})
}

func TestMemoriesRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	var createdID string
	t.Run("Create", func(t *testing.T) {
		mem := domain.Memory{
			Type:    domain.MemoryNote,
			Scope:   domain.ScopePersonal,
			Subject: domain.Subject{Service: "order-service"},
			Source:  domain.MemorySource{Kind: "user"},
			Status:  domain.MemoryActive,
			Text:    "a fresh test memory",
		}
		got, err := h.rc.Memories().Create(ctx, mem)
		require.NoError(t, err)
		createdID = got.ID
		require.NotEmpty(t, createdID)
		want, err := h.fake.Memories().Get(ctx, createdID)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("Get", func(t *testing.T) {
		got, err := h.rc.Memories().Get(ctx, createdID)
		require.NoError(t, err)
		want, err := h.fake.Memories().Get(ctx, createdID)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("GetNotFound", func(t *testing.T) {
		_, err := h.rc.Memories().Get(ctx, "mem_does_not_exist")
		require.Error(t, err)
		assert.Equal(t, errs.MemoryNotFound, errs.CodeOf(err))
	})

	t.Run("List", func(t *testing.T) {
		got, err := h.rc.Memories().List(ctx, domain.MemoryQuery{})
		require.NoError(t, err)
		want, err := h.fake.Memories().List(ctx, domain.MemoryQuery{})
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("ListWithFullQuery", func(t *testing.T) {
		q := domain.MemoryQuery{
			Scope: domain.ScopeService, Type: domain.MemoryInvariant,
			Service: "rider-service", Operation: "rider-service.getRider",
			Limit: 5, MinScore: 0,
		}
		got, err := h.rc.Memories().List(ctx, q)
		require.NoError(t, err)
		want, err := h.fake.Memories().List(ctx, q)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("ListFilteredByFlow", func(t *testing.T) {
		q := domain.MemoryQuery{Flow: "create-order-flow", MinScore: 0.1}
		got, err := h.rc.Memories().List(ctx, q)
		require.NoError(t, err)
		want, err := h.fake.Memories().List(ctx, q)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("Search", func(t *testing.T) {
		q := domain.MemoryQuery{Text: "customerId"}
		got, err := h.rc.Memories().Search(ctx, q)
		require.NoError(t, err)
		want, err := h.fake.Memories().Search(ctx, q)
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.NotEmpty(t, got)
	})

	t.Run("Relevant", func(t *testing.T) {
		subjects := []domain.Subject{{Service: "order-service", Operation: "order-service.createOrder"}}
		got, err := h.rc.Memories().Relevant(ctx, subjects, 10)
		require.NoError(t, err)
		want, err := h.fake.Memories().Relevant(ctx, subjects, 10)
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.NotEmpty(t, got)
	})

	t.Run("Update", func(t *testing.T) {
		mem, err := h.fake.Memories().Get(ctx, createdID)
		require.NoError(t, err)
		mem.Text = "an updated test memory"
		got, err := h.rc.Memories().Update(ctx, *mem)
		require.NoError(t, err)
		want, err := h.fake.Memories().Get(ctx, createdID)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("Promotion", func(t *testing.T) {
		got, err := h.rc.Memories().PromotionTarget(ctx, createdID)
		require.NoError(t, err)
		want, err := h.fake.Memories().PromotionTarget(ctx, createdID)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("Reindex", func(t *testing.T) {
		require.NoError(t, h.rc.Memories().Reindex(ctx))
	})

	t.Run("Delete", func(t *testing.T) {
		require.NoError(t, h.rc.Memories().Delete(ctx, createdID))
		_, err := h.fake.Memories().Get(ctx, createdID)
		require.Error(t, err)
		assert.Equal(t, errs.MemoryNotFound, errs.CodeOf(err))
	})
}

func TestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	req := domain.ContextRequest{Intent: "how do I create an order", Operations: []string{"order-service.createOrder"}}
	got, err := h.rc.Context().Build(ctx, req)
	require.NoError(t, err)
	want, err := h.fake.Context().Build(ctx, req)
	require.NoError(t, err)
	assertSame(t, want, got)
}

func TestEnvsRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	t.Run("List", func(t *testing.T) {
		got, err := h.rc.Envs().List(ctx)
		require.NoError(t, err)
		want, err := h.fake.Envs().List(ctx)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("Get", func(t *testing.T) {
		got, err := h.rc.Envs().Get(ctx, "local")
		require.NoError(t, err)
		want, err := h.fake.Envs().Get(ctx, "local")
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("GetNotFound", func(t *testing.T) {
		_, err := h.rc.Envs().Get(ctx, "no-such-env")
		require.Error(t, err)
		assert.Equal(t, errs.EnvNotFound, errs.CodeOf(err))
	})

	t.Run("Default", func(t *testing.T) {
		got, err := h.rc.Envs().Default(ctx)
		require.NoError(t, err)
		want, err := h.fake.Envs().Default(ctx)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("SetDefault", func(t *testing.T) {
		require.NoError(t, h.rc.Envs().SetDefault(ctx, "production"))
		want, err := h.fake.Envs().Default(ctx)
		require.NoError(t, err)
		assert.Equal(t, "production", want)
	})

	t.Run("SetSecret", func(t *testing.T) {
		require.NoError(t, h.rc.Envs().SetSecret(ctx, "API_KEY", "s3kret"))
		names, err := h.fake.Envs().ListSecrets(ctx)
		require.NoError(t, err)
		assert.Contains(t, names, "API_KEY")
	})

	t.Run("ListSecrets", func(t *testing.T) {
		got, err := h.rc.Envs().ListSecrets(ctx)
		require.NoError(t, err)
		want, err := h.fake.Envs().ListSecrets(ctx)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("DeleteSecret", func(t *testing.T) {
		require.NoError(t, h.rc.Envs().DeleteSecret(ctx, "API_KEY"))
		names, err := h.fake.Envs().ListSecrets(ctx)
		require.NoError(t, err)
		assert.NotContains(t, names, "API_KEY")
	})

	t.Run("DeleteSecretNotFound", func(t *testing.T) {
		err := h.rc.Envs().DeleteSecret(ctx, "API_KEY")
		require.Error(t, err)
		assert.Equal(t, errs.SecretMissing, errs.CodeOf(err))
	})
}

// TestEventsRoundTrip covers Events().Subscribe: dial, forward 3 published
// events in order, then confirm cancel closes the channel.
func TestEventsRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, unsub := h.rc.Events().Subscribe(ctx)
	defer unsub()

	// The WebSocket dial happens in a background goroutine; wait for it to
	// be live before publishing the events under test, since the fake's
	// bus drops events published before any subscriber is registered.
	require.Eventually(t, func() bool {
		h.fake.Publish(domain.Event{Type: domain.EventCatalogChanged, Time: time.Now().UTC(), Payload: "ping"})
		select {
		case ev := <-ch:
			return ev.Payload == "ping"
		case <-time.After(20 * time.Millisecond):
			return false
		}
	}, 2*time.Second, 20*time.Millisecond)

	want := []domain.Event{
		{Type: domain.EventCatalogChanged, Time: time.Now().UTC()},
		{Type: domain.EventRunStarted, Time: time.Now().UTC(), Payload: "run_1"},
		{Type: domain.EventRunFinished, Time: time.Now().UTC(), Payload: "run_1"},
	}
	for _, ev := range want {
		h.fake.Publish(ev)
	}

	got := make([]domain.Event, 0, 3)
	for i := 0; i < 3; i++ {
		select {
		case ev := <-ch:
			got = append(got, ev)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
	assertSame(t, want, got)

	unsub()
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should close after unsubscribe")
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close after unsubscribe")
	}
}

// TestErrorPassthrough confirms an *errs.Error raised by the engine (Code,
// Message, Details) survives the HTTP round trip unchanged.
func TestErrorPassthrough(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	_, err := h.rc.Catalog().GetOperation(ctx, "nope.doesNotExist")
	require.Error(t, err)
	assert.Equal(t, errs.OperationNotFound, errs.CodeOf(err))

	e := errs.As(err)
	assert.Equal(t, "nope.doesNotExist", e.Details["id"])
	assert.NotEmpty(t, e.Message)
}

var _ engine.Engine = (*remote.Remote)(nil)
