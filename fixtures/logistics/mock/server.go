package mock

import (
	"net/http/httptest"
	"testing"
)

// StartAll starts httptest servers for all three logistics mock services,
// backed by a single shared World, for use by other packages' tests (or
// standalone tools). If opts.World is nil, a freshly seeded World is
// created. If tb is non-nil, the servers are registered for automatic
// cleanup via tb.Cleanup(stop); pass nil to manage shutdown manually.
func StartAll(tb testing.TB, opts Options) (orderURL, allocURL, riderURL string, stop func()) {
	world := opts.World
	if world == nil {
		world = NewWorld()
	}
	svcOpts := Options{World: world, RequireAuth: opts.RequireAuth}

	orderSrv := httptest.NewServer(NewOrderService(svcOpts))
	allocSrv := httptest.NewServer(NewAllocationService(svcOpts))
	riderSrv := httptest.NewServer(NewRiderService(svcOpts))

	stop = func() {
		orderSrv.Close()
		allocSrv.Close()
		riderSrv.Close()
	}
	if tb != nil {
		tb.Cleanup(stop)
	}
	return orderSrv.URL, allocSrv.URL, riderSrv.URL, stop
}
