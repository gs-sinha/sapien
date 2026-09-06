package remote_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
	"github.com/growsimplee/sapien/internal/errs"
)

// TestExamplesRoundTrip exercises every engine.ExampleAPI method through a
// real internal/server (backed by enginetest.Fake) and the real
// internal/engine/remote client, so it proves the wire format for each
// method matches what internal/server's handlers (internal/server/
// handlers_examples.go) actually produce and consume -- not just that the
// two packages' Go types agree.
func TestExamplesRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	var createdID string
	t.Run("Create", func(t *testing.T) {
		ex := domain.SavedExample{
			ID:        "create-qcom-order",
			Operation: "order-service.createOrder",
			Body:      map[string]any{"customerId": "cust_1", "type": "QCOM"},
			Headers:   map[string]string{"X-Trace": "abc"},
			Expect:    &domain.ExampleExpect{Status: 201, Body: map[string]any{"orderId": "order_1"}},
			Tags:      []string{"qcom", "happy-path"},
		}
		got, err := h.rc.Examples().Create(ctx, ex)
		require.NoError(t, err)
		createdID = got.ID
		require.NotEmpty(t, createdID)
		want, err := h.fake.Examples().Get(ctx, createdID)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("CreateConflict", func(t *testing.T) {
		ex := domain.SavedExample{ID: createdID, Operation: "order-service.createOrder"}
		_, err := h.rc.Examples().Create(ctx, ex)
		require.Error(t, err)
		assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	})

	t.Run("Get", func(t *testing.T) {
		got, err := h.rc.Examples().Get(ctx, createdID)
		require.NoError(t, err)
		want, err := h.fake.Examples().Get(ctx, createdID)
		require.NoError(t, err)
		assertSame(t, want, got)
	})

	t.Run("GetNotFound", func(t *testing.T) {
		_, err := h.rc.Examples().Get(ctx, "does-not-exist")
		require.Error(t, err)
		assert.Equal(t, errs.ExampleNotFound, errs.CodeOf(err))
	})

	t.Run("List", func(t *testing.T) {
		got, err := h.rc.Examples().List(ctx, domain.ExampleQuery{})
		require.NoError(t, err)
		want, err := h.fake.Examples().List(ctx, domain.ExampleQuery{})
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.NotEmpty(t, got)
	})

	t.Run("ListWithFullQuery", func(t *testing.T) {
		q := domain.ExampleQuery{Operation: "order-service.createOrder", Service: "order-service", Tag: "qcom", Text: "qcom", Limit: 5}
		got, err := h.rc.Examples().List(ctx, q)
		require.NoError(t, err)
		want, err := h.fake.Examples().List(ctx, q)
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.NotEmpty(t, got)
	})

	t.Run("ForOperations", func(t *testing.T) {
		got, err := h.rc.Examples().ForOperations(ctx, []string{"order-service.createOrder", "order-service.getOrder"}, 10)
		require.NoError(t, err)
		want, err := h.fake.Examples().ForOperations(ctx, []string{"order-service.createOrder", "order-service.getOrder"}, 10)
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.NotEmpty(t, got)
	})

	t.Run("Update", func(t *testing.T) {
		ex, err := h.fake.Examples().Get(ctx, createdID)
		require.NoError(t, err)
		ex.Description = "an updated description"
		got, err := h.rc.Examples().Update(ctx, *ex)
		require.NoError(t, err)
		want, err := h.fake.Examples().Get(ctx, createdID)
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.Equal(t, "an updated description", got.Description)
	})

	t.Run("UpdateNotFound", func(t *testing.T) {
		_, err := h.rc.Examples().Update(ctx, domain.SavedExample{ID: "does-not-exist", Operation: "order-service.createOrder"})
		require.Error(t, err)
		assert.Equal(t, errs.ExampleNotFound, errs.CodeOf(err))
	})

	var fromRunID string
	t.Run("FromRun", func(t *testing.T) {
		req := engine.CallRequest{
			Operation: "order-service.getOrder",
			Params:    map[string]any{"orderId": "order_5"},
			Env:       "local",
			Trigger:   "cli",
		}
		run, err := h.rc.Runner().Call(ctx, req)
		require.NoError(t, err)

		fromRunReq := engine.ExampleFromRun{
			RunID:       run.ID,
			StepID:      "call",
			ID:          "from-run-example",
			Description: "captured from a real call",
			Tags:        []string{"verified"},
			Source:      &domain.MemorySource{Kind: "agent", Client: "claude-code"},
		}
		got, err := h.rc.Examples().FromRun(ctx, fromRunReq)
		require.NoError(t, err)
		fromRunID = got.ID
		want, err := h.fake.Examples().Get(ctx, fromRunID)
		require.NoError(t, err)
		assertSame(t, want, got)

		require.NotNil(t, got.Verified)
		assert.Equal(t, run.ID, got.Verified.RunID)
		assert.Equal(t, "call", got.Verified.StepID)
		assert.Equal(t, "agent", got.Verified.Source.Kind)
	})

	t.Run("FromRunNotFound", func(t *testing.T) {
		_, err := h.rc.Examples().FromRun(ctx, engine.ExampleFromRun{RunID: "run_does_not_exist", ID: "x"})
		require.Error(t, err)
		assert.Equal(t, errs.RunNotFound, errs.CodeOf(err))
	})

	t.Run("Reindex", func(t *testing.T) {
		require.NoError(t, h.rc.Examples().Reindex(ctx))
	})

	t.Run("Delete", func(t *testing.T) {
		require.NoError(t, h.rc.Examples().Delete(ctx, createdID))
		_, err := h.fake.Examples().Get(ctx, createdID)
		require.Error(t, err)
		assert.Equal(t, errs.ExampleNotFound, errs.CodeOf(err))

		require.NoError(t, h.rc.Examples().Delete(ctx, fromRunID))
	})

	t.Run("DeleteNotFound", func(t *testing.T) {
		err := h.rc.Examples().Delete(ctx, "does-not-exist")
		require.Error(t, err)
		assert.Equal(t, errs.ExampleNotFound, errs.CodeOf(err))
	})
}
