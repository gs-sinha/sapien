// NOTE: this file uses only the standard library, not testify - see the
// comment at the top of mock_test.go for why.
package mock

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// specFiles lists every openapi.yaml this fixture ships, relative to this
// package's directory.
var specFiles = []string{
	filepath.Join("..", "order-service", "api", "openapi.yaml"),
	filepath.Join("..", "allocation-service", "api", "openapi.yaml"),
	filepath.Join("..", "rider-service", "api", "openapi.yaml"),
}

// TestOpenAPISpecsExistAndAreNonEmpty is the item-1 validation required by
// the fixtures task: this package has no YAML library available (only the
// standard library plus testify, and testify is unusable here - see
// mock_test.go), so it cannot parse the specs into JSON to check
// structural validity directly. Instead it asserts the files exist and
// are non-empty and start with an openapi: document, and
// TestMockRoutesCoverSpecPaths below hard-codes the expected route table
// (kept in sync by hand with the three openapi.yaml files) and confirms
// the mock servers implement every one of those routes.
func TestOpenAPISpecsExistAndAreNonEmpty(t *testing.T) {
	for _, path := range specFiles {
		t.Run(path, func(t *testing.T) {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("spec file must exist: %s: %v", path, err)
			}
			if info.Size() == 0 {
				t.Fatalf("spec file must be non-empty: %s", path)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read spec file %s: %v", path, err)
			}
			if !strings.HasPrefix(strings.TrimSpace(string(data)), "openapi:") {
				t.Errorf("spec file must start with an openapi: document: %s", path)
			}
		})
	}
}

// routeCase is one hard-coded (method, concrete path) pair taken from a
// service's openapi.yaml, with path parameters substituted by a
// placeholder value so it can be dispatched to a running mock server.
type routeCase struct {
	method string
	path   string
	body   string // JSON request body, or "" for none
}

// orderServiceRoutes mirrors every path+method in
// order-service/api/openapi.yaml.
var orderServiceRoutes = []routeCase{
	{http.MethodPost, "/v1/orders", `{"customerId":"c1","type":"STANDARD","pickup":{"lat":1,"lng":1},"drop":{"lat":1,"lng":1}}`},
	{http.MethodGet, "/v1/orders", ""},
	{http.MethodGet, "/v1/orders/ord_dummy", ""},
	{http.MethodPost, "/v1/orders/ord_dummy/cancel", ""},
	{http.MethodGet, "/v1/orders/ord_dummy/timeline", ""},
}

// allocationServiceRoutes mirrors every path+method in
// allocation-service/api/openapi.yaml.
var allocationServiceRoutes = []routeCase{
	{http.MethodPost, "/v1/allocations", `{"orderId":"ord_dummy"}`},
	{http.MethodGet, "/v1/allocations", ""},
	{http.MethodGet, "/v1/allocations/stats", ""},
	{http.MethodGet, "/v1/allocations/alloc_dummy", ""},
	{http.MethodPost, "/v1/allocations/alloc_dummy/release", ""},
	{http.MethodPost, "/v1/allocate", `{"orderId":"ord_dummy"}`},
}

// riderServiceRoutes mirrors every path+method in
// rider-service/api/openapi.yaml.
var riderServiceRoutes = []routeCase{
	{http.MethodGet, "/v1/riders/R123", ""},
	{http.MethodPost, "/v1/riders/search", `{"lat":1,"lng":1,"radiusKm":1}`},
	{http.MethodPatch, "/v1/riders/R123/status", `{"online":true}`},
	{http.MethodGet, "/v1/riders", ""},
}

// TestMockRoutesCoverSpecPaths confirms every path+method hard-coded above
// (kept in sync by hand with the three services' openapi.yaml files,
// since this package deliberately has no YAML parser available) is wired
// up in the corresponding mock handler. A request that reaches our
// handler always gets a JSON body (even for 4xx/5xx); a request that
// misses every registered route falls through to Go's default 404, which
// is "text/plain; charset=utf-8". Asserting Content-Type is
// application/json therefore proves the route exists, independent of
// whether the specific dummy IDs used here happen to resolve to a real
// resource.
func TestMockRoutesCoverSpecPaths(t *testing.T) {
	cases := map[string][]routeCase{
		"order-service":      orderServiceRoutes,
		"allocation-service": allocationServiceRoutes,
		"rider-service":      riderServiceRoutes,
	}

	world := NewWorld()
	handlers := map[string]http.Handler{
		"order-service":      NewOrderService(Options{World: world}),
		"allocation-service": NewAllocationService(Options{World: world}),
		"rider-service":      NewRiderService(Options{World: world}),
	}

	for service, routes := range cases {
		for _, rc := range routes {
			t.Run(service+" "+rc.method+" "+rc.path, func(t *testing.T) {
				var body *bytes.Reader
				if rc.body != "" {
					body = bytes.NewReader([]byte(rc.body))
				} else {
					body = bytes.NewReader(nil)
				}
				req := httptest.NewRequest(rc.method, rc.path, body)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()

				handlers[service].ServeHTTP(rec, req)

				ct := rec.Header().Get("Content-Type")
				if !hasJSONContentType(ct) {
					t.Errorf("%s %s %s: expected a route to be registered (Content-Type: application/json), got Content-Type %q status %d", service, rc.method, rc.path, ct, rec.Code)
				}
			})
		}
	}
}

func hasJSONContentType(ct string) bool {
	return strings.HasPrefix(ct, "application/json")
}

// TestMockRoutesRejectUnknownPath is a control for
// TestMockRoutesCoverSpecPaths: it confirms a path absent from every spec
// really does fall through to the default (non-JSON) 404, so the
// Content-Type check above is meaningful.
func TestMockRoutesRejectUnknownPath(t *testing.T) {
	world := NewWorld()
	handler := NewOrderService(Options{World: world})

	req := httptest.NewRequest(http.MethodGet, "/v1/nonexistent", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if hasJSONContentType(rec.Header().Get("Content-Type")) {
		t.Errorf("Content-Type = %q, want a non-JSON default 404", rec.Header().Get("Content-Type"))
	}
}
