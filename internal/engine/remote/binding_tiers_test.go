package remote_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// TestServicesBindingRoundTrip proves the wire format of Bind, Unbind and
// Binding matches what internal/server's handlers produce and consume, the
// way TestServicesRoundTrip does for the older ServiceAPI methods: the
// remote client's answer must equal the fake's own.
func TestServicesBindingRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	t.Run("BindingBeforeBind", func(t *testing.T) {
		got, err := h.rc.Services().Binding(ctx, "order-service")
		require.NoError(t, err)
		want, err := h.fake.Services().Binding(ctx, "order-service")
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.Equal(t, "order-service", got.Service)
		assert.Equal(t, domain.BindingLocal, got.Binding.Mode)
	})

	t.Run("BindingNotFound", func(t *testing.T) {
		_, err := h.rc.Services().Binding(ctx, "no-such-service")
		require.Error(t, err)
		assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
	})

	t.Run("Bind", func(t *testing.T) {
		got, err := h.rc.Services().Bind(ctx, "order-service", "/home/dev/code/order-service")
		require.NoError(t, err)
		want, err := h.fake.Services().Get(ctx, "order-service")
		require.NoError(t, err)
		assertSame(t, want, got)
		require.NotNil(t, got.Binding)
		assert.Equal(t, domain.BindingLocal, got.Binding.Mode)
		assert.Equal(t, "/home/dev/code/order-service", got.Binding.Local.Path)
		assert.True(t, got.Binding.Writable)

		info, err := h.rc.Services().Binding(ctx, "order-service")
		require.NoError(t, err)
		assert.Equal(t, "/home/dev/code/order-service", info.Binding.Local.Path)
	})

	t.Run("BindNotFound", func(t *testing.T) {
		_, err := h.rc.Services().Bind(ctx, "no-such-service", "/x")
		require.Error(t, err)
		assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
	})

	t.Run("BindEmptyPathIsInvalid", func(t *testing.T) {
		_, err := h.rc.Services().Bind(ctx, "order-service", "")
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})

	t.Run("Unbind", func(t *testing.T) {
		got, err := h.rc.Services().Unbind(ctx, "order-service")
		require.NoError(t, err)
		want, err := h.fake.Services().Get(ctx, "order-service")
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.Equal(t, "services/order-service", got.Source.Path, "the committed source is read again")
	})

	t.Run("UnbindNotFound", func(t *testing.T) {
		_, err := h.rc.Services().Unbind(ctx, "no-such-service")
		require.Error(t, err)
		assert.Equal(t, errs.ServiceNotFound, errs.CodeOf(err))
	})
}

// TestFlowTiersRoundTrip does the same for CreateIn and Rescope.
func TestFlowTiersRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	src := "version: 1\nid: tier-flow\nsteps:\n  - id: a\n    call: order-service.createOrder\n"

	t.Run("CreateInLocal", func(t *testing.T) {
		got, err := h.rc.Flows().CreateIn(ctx, src, engine.CreateFlowOptions{Path: "tier-flow.flow.yaml", OwnerKind: domain.FlowOwnerLocal})
		require.NoError(t, err)
		want, err := h.fake.Flows().Get(ctx, "tier-flow")
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.Equal(t, domain.FlowOwnerLocal, got.OwnerKind)
	})

	// An empty OwnerKind on the wire is the pre-tier body, which the server
	// keeps routing to Create (workspace tier) for older clients; the remote
	// client therefore must not rely on the engine's local default and
	// callers that want local say so. This pins that the two agree.
	t.Run("CreateInEmptyOwnerIsWorkspaceOnTheWire", func(t *testing.T) {
		got, err := h.rc.Flows().CreateIn(ctx, "version: 1\nid: untiered\nsteps: []\n", engine.CreateFlowOptions{})
		require.NoError(t, err)
		assert.Equal(t, domain.FlowOwnerWorkspace, got.OwnerKind)
	})

	t.Run("CreateInService", func(t *testing.T) {
		got, err := h.rc.Flows().CreateIn(ctx, "version: 1\nid: svc-tier\nsteps: []\n", engine.CreateFlowOptions{OwnerKind: domain.FlowOwnerService, OwnerID: "order-service"})
		require.NoError(t, err)
		want, err := h.fake.Flows().Get(ctx, "svc-tier")
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.Equal(t, domain.FlowOwnerService, got.OwnerKind)
		assert.Equal(t, "order-service", got.OwnerID)
	})

	t.Run("CreateInConflict", func(t *testing.T) {
		_, err := h.rc.Flows().CreateIn(ctx, src, engine.CreateFlowOptions{OwnerKind: domain.FlowOwnerLocal})
		require.Error(t, err)
		assert.Equal(t, errs.Conflict, errs.CodeOf(err))
	})

	t.Run("Rescope", func(t *testing.T) {
		got, err := h.rc.Flows().Rescope(ctx, "tier-flow", domain.FlowOwnerWorkspace, "")
		require.NoError(t, err)
		want, err := h.fake.Flows().Get(ctx, "tier-flow")
		require.NoError(t, err)
		assertSame(t, want, got)
		assert.Equal(t, domain.FlowOwnerWorkspace, got.OwnerKind)

		got, err = h.rc.Flows().Rescope(ctx, "tier-flow", domain.FlowOwnerService, "order-service")
		require.NoError(t, err)
		assert.Equal(t, domain.FlowOwnerService, got.OwnerKind)
		assert.Equal(t, "order-service", got.OwnerID)

		summaries, err := h.rc.Flows().List(ctx, "tier-flow")
		require.NoError(t, err)
		require.Len(t, summaries, 1)
		assert.Equal(t, domain.FlowOwnerService, summaries[0].OwnerKind)
		assert.Equal(t, "order-service", summaries[0].OwnerID)
	})

	t.Run("RescopeNotFound", func(t *testing.T) {
		_, err := h.rc.Flows().Rescope(ctx, "no-such-flow", domain.FlowOwnerLocal, "")
		require.Error(t, err)
		assert.Equal(t, errs.FlowNotFound, errs.CodeOf(err))
	})

	t.Run("RescopeEmptyOwnerKindIsInvalid", func(t *testing.T) {
		_, err := h.rc.Flows().Rescope(ctx, "tier-flow", "", "")
		require.Error(t, err)
		assert.Equal(t, errs.Invalid, errs.CodeOf(err))
	})
}
