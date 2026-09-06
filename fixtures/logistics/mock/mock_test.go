// NOTE: testify is unusable in this checkout: github.com/stretchr/testify
// v1.12.1 has its assert package unconditionally import
// "github.com/stretchr/testify/assert/yaml", which needs go.yaml.in/yaml/v3,
// and go.sum is missing that module's zip hash (only a go.mod-hash line is
// present). `go build`/`go vet` fail with "missing go.sum entry for module
// providing package go.yaml.in/yaml/v3" for any file that imports
// testify/assert or testify/require. Fixing that requires editing go.sum,
// which is out of scope here (see the final report), so these tests use
// only the standard library, following the precedent set in
// internal/store/store_test.go.
package mock

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"
)

// doJSON performs an HTTP request with an optional JSON body and optional
// extra headers, and decodes a JSON object response body (if any).
func doJSON(t *testing.T, method, url string, body any, headers map[string]string) (*http.Response, map[string]any) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal response body %q: %v", raw, err)
		}
	}
	return resp, out
}

func newTestWorld(t *testing.T) *World {
	t.Helper()
	w := NewWorld()
	w.SetTimelineDelay(0)
	return w
}

func requireStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("status = %d, want %d", resp.StatusCode, want)
	}
}

func checkEqual(t *testing.T, name string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", name, got, want)
	}
}

func requireNonEmptyString(t *testing.T, name string, v any) string {
	t.Helper()
	s, ok := v.(string)
	if !ok || s == "" {
		t.Fatalf("%s = %#v, want a non-empty string", name, v)
	}
	return s
}

// TestSuccessScenario_QCOMAllocation walks PRD §48's core loop over real
// HTTP: create a QCOM order, allocate a rider, fetch that rider, and
// verify it is online and eligible for QCOM (qcomSkill=true).
func TestSuccessScenario_QCOMAllocation(t *testing.T) {
	world := newTestWorld(t)
	orderURL, allocURL, riderURL, _ := StartAll(t, Options{World: world})

	resp, order := doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
		"customerId": "cust_1",
		"type":       "QCOM",
		"pickup":     map[string]float64{"lat": 12.9716, "lng": 77.5946},
		"drop":       map[string]float64{"lat": 12.9352, "lng": 77.6146},
	}, nil)
	requireStatus(t, resp, http.StatusCreated)
	orderID := requireNonEmptyString(t, "orderId", order["orderId"])
	checkEqual(t, "status", order["status"], "CREATED")
	checkEqual(t, "type", order["type"], "QCOM")

	resp, alloc := doJSON(t, http.MethodPost, allocURL+"/v1/allocations", map[string]any{"orderId": orderID}, nil)
	requireStatus(t, resp, http.StatusCreated)
	riderID := requireNonEmptyString(t, "riderId", alloc["riderId"])
	checkEqual(t, "status", alloc["status"], "ALLOCATED")
	checkEqual(t, "orderId", alloc["orderId"], orderID)

	resp, rider := doJSON(t, http.MethodGet, riderURL+"/v1/riders/"+riderID, nil, nil)
	requireStatus(t, resp, http.StatusOK)
	checkEqual(t, "online", rider["online"], true)
	checkEqual(t, "qcomSkill", rider["qcomSkill"], true)

	// The order should now reflect ALLOCATED status.
	resp, gotOrder := doJSON(t, http.MethodGet, orderURL+"/v1/orders/"+orderID, nil, nil)
	requireStatus(t, resp, http.StatusOK)
	checkEqual(t, "status", gotOrder["status"], "ALLOCATED")
}

// TestAllocate_STANDARD_AnyOnlineRiderEligible confirms STANDARD orders may
// be matched to any online rider, qcomSkill or not.
func TestAllocate_STANDARD_AnyOnlineRiderEligible(t *testing.T) {
	world := newTestWorld(t)
	orderURL, allocURL, _, _ := StartAll(t, Options{World: world})

	_, order := doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
		"customerId": "cust_2",
		"type":       "STANDARD",
		"pickup":     map[string]float64{"lat": 1, "lng": 1},
		"drop":       map[string]float64{"lat": 2, "lng": 2},
	}, nil)
	orderID := requireNonEmptyString(t, "orderId", order["orderId"])

	resp, alloc := doJSON(t, http.MethodPost, allocURL+"/v1/allocations", map[string]any{"orderId": orderID}, nil)
	requireStatus(t, resp, http.StatusCreated)
	requireNonEmptyString(t, "riderId", alloc["riderId"])
}

// TestAllocate_NoRiderAvailable_QCOM confirms that once every qcomSkill
// rider is taken offline, a QCOM allocation fails with 409
// NO_RIDER_AVAILABLE.
func TestAllocate_NoRiderAvailable_QCOM(t *testing.T) {
	world := newTestWorld(t)
	orderURL, allocURL, riderURL, _ := StartAll(t, Options{World: world})

	// Seed has two qcomSkill=true riders online by default: R123
	// (Bangalore) and R126 (Mumbai); R125 (qcomSkill=true) starts offline.
	// Take every online qcomSkill rider offline.
	for _, riderID := range []string{"R123", "R126"} {
		resp, _ := doJSON(t, http.MethodPatch, riderURL+"/v1/riders/"+riderID+"/status", map[string]any{"online": false}, nil)
		requireStatus(t, resp, http.StatusOK)
	}

	_, order := doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
		"customerId": "cust_3",
		"type":       "QCOM",
		"pickup":     map[string]float64{"lat": 1, "lng": 1},
		"drop":       map[string]float64{"lat": 2, "lng": 2},
	}, nil)
	orderID := requireNonEmptyString(t, "orderId", order["orderId"])

	resp, errBody := doJSON(t, http.MethodPost, allocURL+"/v1/allocations", map[string]any{"orderId": orderID}, nil)
	requireStatus(t, resp, http.StatusConflict)
	checkEqual(t, "code", errBody["code"], CodeNoRiderAvailable)
}

// TestAllocate_NoRiderAvailable_ByExhaustingBusyRiders confirms allocation
// also fails once every eligible rider is already busy on another order
// (not merely offline).
func TestAllocate_NoRiderAvailable_ByExhaustingBusyRiders(t *testing.T) {
	world := newTestWorld(t)
	orderURL, allocURL, riderURL, _ := StartAll(t, Options{World: world})

	// Bring the offline qcomSkill rider (R125) online so all three
	// qcomSkill riders (R123, R125, R126) are online, then allocate three
	// QCOM orders to exhaust all three.
	resp, _ := doJSON(t, http.MethodPatch, riderURL+"/v1/riders/R125/status", map[string]any{"online": true}, nil)
	requireStatus(t, resp, http.StatusOK)

	for i := 0; i < 3; i++ {
		_, order := doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
			"customerId": "cust_busy",
			"type":       "QCOM",
			"pickup":     map[string]float64{"lat": 1, "lng": 1},
			"drop":       map[string]float64{"lat": 2, "lng": 2},
		}, nil)
		orderID := requireNonEmptyString(t, "orderId", order["orderId"])
		resp, _ := doJSON(t, http.MethodPost, allocURL+"/v1/allocations", map[string]any{"orderId": orderID}, nil)
		requireStatus(t, resp, http.StatusCreated)
	}

	_, order := doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
		"customerId": "cust_busy",
		"type":       "QCOM",
		"pickup":     map[string]float64{"lat": 1, "lng": 1},
		"drop":       map[string]float64{"lat": 2, "lng": 2},
	}, nil)
	orderID := requireNonEmptyString(t, "orderId", order["orderId"])

	resp, errBody := doJSON(t, http.MethodPost, allocURL+"/v1/allocations", map[string]any{"orderId": orderID}, nil)
	requireStatus(t, resp, http.StatusConflict)
	checkEqual(t, "code", errBody["code"], CodeNoRiderAvailable)
}

// TestCancelOrder_ReleasesAllocation confirms cancelling an order frees its
// rider by releasing the allocation, and that the freed rider becomes
// eligible for allocation again.
func TestCancelOrder_ReleasesAllocation(t *testing.T) {
	world := newTestWorld(t)
	orderURL, allocURL, _, _ := StartAll(t, Options{World: world})

	_, order := doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
		"customerId": "cust_4",
		"type":       "QCOM",
		"pickup":     map[string]float64{"lat": 1, "lng": 1},
		"drop":       map[string]float64{"lat": 2, "lng": 2},
	}, nil)
	orderID := requireNonEmptyString(t, "orderId", order["orderId"])

	_, alloc := doJSON(t, http.MethodPost, allocURL+"/v1/allocations", map[string]any{"orderId": orderID}, nil)
	allocationID := requireNonEmptyString(t, "allocationId", alloc["allocationId"])
	riderID := requireNonEmptyString(t, "riderId", alloc["riderId"])

	resp, cancelled := doJSON(t, http.MethodPost, orderURL+"/v1/orders/"+orderID+"/cancel", nil, nil)
	requireStatus(t, resp, http.StatusOK)
	checkEqual(t, "status", cancelled["status"], "CANCELLED")

	resp, gotAlloc := doJSON(t, http.MethodGet, allocURL+"/v1/allocations/"+allocationID, nil, nil)
	requireStatus(t, resp, http.StatusOK)
	checkEqual(t, "status", gotAlloc["status"], "RELEASED")

	// The freed rider should be selectable again for a fresh QCOM order.
	_, order2 := doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
		"customerId": "cust_4",
		"type":       "QCOM",
		"pickup":     map[string]float64{"lat": 1, "lng": 1},
		"drop":       map[string]float64{"lat": 2, "lng": 2},
	}, nil)
	order2ID := requireNonEmptyString(t, "orderId", order2["orderId"])
	resp, alloc2 := doJSON(t, http.MethodPost, allocURL+"/v1/allocations", map[string]any{"orderId": order2ID}, nil)
	requireStatus(t, resp, http.StatusCreated)
	if alloc2["riderId"] != riderID {
		t.Errorf("riderId = %v, want %v (the rider freed by cancellation should be reused before any other eligible rider)", alloc2["riderId"], riderID)
	}
}

// TestCancelOrder_AlreadyDelivered confirms 409 ORDER_ALREADY_DELIVERED.
func TestCancelOrder_AlreadyDelivered(t *testing.T) {
	world := newTestWorld(t)
	orderURL, _, _, _ := StartAll(t, Options{World: world})

	_, order := doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
		"customerId": "cust_5",
		"type":       "STANDARD",
		"pickup":     map[string]float64{"lat": 1, "lng": 1},
		"drop":       map[string]float64{"lat": 2, "lng": 2},
	}, nil)
	orderID := requireNonEmptyString(t, "orderId", order["orderId"])

	// White-box: simulate delivery directly, since no HTTP operation
	// reaches DELIVERED in this fixture.
	world.mu.Lock()
	world.orders[orderID].status = "DELIVERED"
	world.mu.Unlock()

	resp, errBody := doJSON(t, http.MethodPost, orderURL+"/v1/orders/"+orderID+"/cancel", nil, nil)
	requireStatus(t, resp, http.StatusConflict)
	checkEqual(t, "code", errBody["code"], CodeOrderAlreadyDelivered)
}

// TestTimeline_NotReadyThenReady confirms the timeline polling contract:
// 404 TIMELINE_NOT_READY immediately after creation, then 200 once the
// delay has elapsed.
func TestTimeline_NotReadyThenReady(t *testing.T) {
	world := NewWorld()
	world.SetTimelineDelay(50 * time.Millisecond)
	orderURL, _, _, _ := StartAll(t, Options{World: world})

	_, order := doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
		"customerId": "cust_6",
		"type":       "STANDARD",
		"pickup":     map[string]float64{"lat": 1, "lng": 1},
		"drop":       map[string]float64{"lat": 2, "lng": 2},
	}, nil)
	orderID := requireNonEmptyString(t, "orderId", order["orderId"])

	resp, errBody := doJSON(t, http.MethodGet, orderURL+"/v1/orders/"+orderID+"/timeline", nil, nil)
	requireStatus(t, resp, http.StatusNotFound)
	checkEqual(t, "code", errBody["code"], CodeTimelineNotReady)

	time.Sleep(75 * time.Millisecond)

	resp, timeline := doJSON(t, http.MethodGet, orderURL+"/v1/orders/"+orderID+"/timeline", nil, nil)
	requireStatus(t, resp, http.StatusOK)
	checkEqual(t, "orderId", timeline["orderId"], orderID)
	events, ok := timeline["events"].([]any)
	if !ok || len(events) == 0 {
		t.Errorf("events = %#v, want a non-empty array", timeline["events"])
	}
}

// TestAuth_RequiredWhenEnabled confirms RequireAuth rejects requests
// without a Bearer token, and accepts one that has it.
func TestAuth_RequiredWhenEnabled(t *testing.T) {
	world := newTestWorld(t)
	orderURL, _, _, _ := StartAll(t, Options{World: world, RequireAuth: true})

	resp, errBody := doJSON(t, http.MethodGet, orderURL+"/v1/orders", nil, nil)
	requireStatus(t, resp, http.StatusUnauthorized)
	checkEqual(t, "code", errBody["code"], CodeUnauthorized)

	resp, _ = doJSON(t, http.MethodGet, orderURL+"/v1/orders", nil, map[string]string{"Authorization": "Bearer test-token"})
	requireStatus(t, resp, http.StatusOK)
}

func TestAuth_OffByDefault(t *testing.T) {
	world := newTestWorld(t)
	orderURL, _, _, _ := StartAll(t, Options{World: world})

	resp, _ := doJSON(t, http.MethodGet, orderURL+"/v1/orders", nil, nil)
	requireStatus(t, resp, http.StatusOK)
}

func TestGetOrder_NotFound(t *testing.T) {
	world := newTestWorld(t)
	orderURL, _, _, _ := StartAll(t, Options{World: world})

	resp, errBody := doJSON(t, http.MethodGet, orderURL+"/v1/orders/ord_9999", nil, nil)
	requireStatus(t, resp, http.StatusNotFound)
	checkEqual(t, "code", errBody["code"], CodeOrderNotFound)
}

func TestCreateOrder_InvalidRequest(t *testing.T) {
	world := newTestWorld(t)
	orderURL, _, _, _ := StartAll(t, Options{World: world})

	resp, errBody := doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{"type": "BOGUS"}, nil)
	requireStatus(t, resp, http.StatusBadRequest)
	checkEqual(t, "code", errBody["code"], CodeInvalidRequest)
}

func TestGetAllocation_NotFound(t *testing.T) {
	world := newTestWorld(t)
	_, allocURL, _, _ := StartAll(t, Options{World: world})

	resp, errBody := doJSON(t, http.MethodGet, allocURL+"/v1/allocations/alloc_9999", nil, nil)
	requireStatus(t, resp, http.StatusNotFound)
	checkEqual(t, "code", errBody["code"], CodeAllocationNotFound)
}

func TestReleaseAllocation_AlreadyReleased(t *testing.T) {
	world := newTestWorld(t)
	orderURL, allocURL, _, _ := StartAll(t, Options{World: world})

	_, order := doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
		"customerId": "cust_7",
		"type":       "STANDARD",
		"pickup":     map[string]float64{"lat": 1, "lng": 1},
		"drop":       map[string]float64{"lat": 2, "lng": 2},
	}, nil)
	orderID := requireNonEmptyString(t, "orderId", order["orderId"])
	_, alloc := doJSON(t, http.MethodPost, allocURL+"/v1/allocations", map[string]any{"orderId": orderID}, nil)
	allocationID := requireNonEmptyString(t, "allocationId", alloc["allocationId"])

	resp, _ := doJSON(t, http.MethodPost, allocURL+"/v1/allocations/"+allocationID+"/release", nil, nil)
	requireStatus(t, resp, http.StatusOK)

	resp, errBody := doJSON(t, http.MethodPost, allocURL+"/v1/allocations/"+allocationID+"/release", nil, nil)
	requireStatus(t, resp, http.StatusConflict)
	checkEqual(t, "code", errBody["code"], CodeAllocationAlreadyReleased)
}

func TestGetRider_NotFound(t *testing.T) {
	world := newTestWorld(t)
	_, _, riderURL, _ := StartAll(t, Options{World: world})

	resp, errBody := doJSON(t, http.MethodGet, riderURL+"/v1/riders/R999", nil, nil)
	requireStatus(t, resp, http.StatusNotFound)
	checkEqual(t, "code", errBody["code"], CodeRiderNotFound)
}

func TestUpdateRiderStatus_NotFound(t *testing.T) {
	world := newTestWorld(t)
	_, _, riderURL, _ := StartAll(t, Options{World: world})

	resp, errBody := doJSON(t, http.MethodPatch, riderURL+"/v1/riders/R999/status", map[string]any{"online": true}, nil)
	requireStatus(t, resp, http.StatusNotFound)
	checkEqual(t, "code", errBody["code"], CodeRiderNotFound)
}

func TestSearchRiders_QcomOnly(t *testing.T) {
	world := newTestWorld(t)
	_, _, riderURL, _ := StartAll(t, Options{World: world})

	resp, body := doJSON(t, http.MethodPost, riderURL+"/v1/riders/search", map[string]any{
		"lat": 12.9, "lng": 77.6, "radiusKm": 5, "qcomOnly": true,
	}, nil)
	requireStatus(t, resp, http.StatusOK)
	riders, ok := body["riders"].([]any)
	if !ok || len(riders) == 0 {
		t.Fatalf("riders = %#v, want a non-empty array", body["riders"])
	}
	for _, ri := range riders {
		rider := ri.(map[string]any)
		checkEqual(t, "qcomSkill", rider["qcomSkill"], true)
	}
}

func TestListRiders_FilterByCityAndOnline(t *testing.T) {
	world := newTestWorld(t)
	_, _, riderURL, _ := StartAll(t, Options{World: world})

	resp, body := doJSON(t, http.MethodGet, riderURL+"/v1/riders?city=Mumbai&online=true", nil, nil)
	requireStatus(t, resp, http.StatusOK)
	riders, ok := body["riders"].([]any)
	if !ok || len(riders) == 0 {
		t.Fatalf("riders = %#v, want a non-empty array", body["riders"])
	}
	for _, ri := range riders {
		rider := ri.(map[string]any)
		checkEqual(t, "city", rider["city"], "Mumbai")
		checkEqual(t, "online", rider["online"], true)
	}
}

func TestListOrders_FilterByCustomerAndStatus(t *testing.T) {
	world := newTestWorld(t)
	orderURL, _, _, _ := StartAll(t, Options{World: world})

	doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
		"customerId": "cust_list", "type": "STANDARD",
		"pickup": map[string]float64{"lat": 1, "lng": 1}, "drop": map[string]float64{"lat": 2, "lng": 2},
	}, nil)
	doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
		"customerId": "cust_other", "type": "STANDARD",
		"pickup": map[string]float64{"lat": 1, "lng": 1}, "drop": map[string]float64{"lat": 2, "lng": 2},
	}, nil)

	resp, body := doJSON(t, http.MethodGet, orderURL+"/v1/orders?customerId=cust_list", nil, nil)
	requireStatus(t, resp, http.StatusOK)
	orders, ok := body["orders"].([]any)
	if !ok || len(orders) != 1 {
		t.Fatalf("orders = %#v, want exactly 1 entry", body["orders"])
	}
	checkEqual(t, "customerId", orders[0].(map[string]any)["customerId"], "cust_list")
}

func TestListAllocations_FilterByOrderAndRider(t *testing.T) {
	world := newTestWorld(t)
	orderURL, allocURL, _, _ := StartAll(t, Options{World: world})

	_, order := doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
		"customerId": "cust_8", "type": "STANDARD",
		"pickup": map[string]float64{"lat": 1, "lng": 1}, "drop": map[string]float64{"lat": 2, "lng": 2},
	}, nil)
	orderID := requireNonEmptyString(t, "orderId", order["orderId"])
	_, alloc := doJSON(t, http.MethodPost, allocURL+"/v1/allocations", map[string]any{"orderId": orderID}, nil)
	riderID := requireNonEmptyString(t, "riderId", alloc["riderId"])

	resp, body := doJSON(t, http.MethodGet, allocURL+"/v1/allocations?orderId="+orderID, nil, nil)
	requireStatus(t, resp, http.StatusOK)
	allocs, ok := body["allocations"].([]any)
	if !ok || len(allocs) != 1 {
		t.Fatalf("allocations = %#v, want exactly 1 entry", body["allocations"])
	}
	checkEqual(t, "riderId", allocs[0].(map[string]any)["riderId"], riderID)
}

func TestAllocationStats(t *testing.T) {
	world := newTestWorld(t)
	orderURL, allocURL, _, _ := StartAll(t, Options{World: world})

	_, order := doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
		"customerId": "cust_9", "type": "STANDARD",
		"pickup": map[string]float64{"lat": 1, "lng": 1}, "drop": map[string]float64{"lat": 2, "lng": 2},
	}, nil)
	orderID := requireNonEmptyString(t, "orderId", order["orderId"])
	doJSON(t, http.MethodPost, allocURL+"/v1/allocations", map[string]any{"orderId": orderID}, nil)

	resp, stats := doJSON(t, http.MethodGet, allocURL+"/v1/allocations/stats", nil, nil)
	requireStatus(t, resp, http.StatusOK)
	checkEqual(t, "total", stats["total"], float64(1))
	checkEqual(t, "allocated", stats["allocated"], float64(1))
	checkEqual(t, "released", stats["released"], float64(0))
}

func TestAllocateV1_Deprecated(t *testing.T) {
	world := newTestWorld(t)
	orderURL, allocURL, _, _ := StartAll(t, Options{World: world})

	_, order := doJSON(t, http.MethodPost, orderURL+"/v1/orders", map[string]any{
		"customerId": "cust_10", "type": "STANDARD",
		"pickup": map[string]float64{"lat": 1, "lng": 1}, "drop": map[string]float64{"lat": 2, "lng": 2},
	}, nil)
	orderID := requireNonEmptyString(t, "orderId", order["orderId"])

	resp, alloc := doJSON(t, http.MethodPost, allocURL+"/v1/allocate", map[string]any{"orderId": orderID}, nil)
	requireStatus(t, resp, http.StatusCreated)
	requireNonEmptyString(t, "riderId", alloc["riderId"])
}

func TestReset_RestoresSeedState(t *testing.T) {
	world := NewWorld()
	world.CreateOrder("cust_x", "STANDARD", LatLng{}, LatLng{})
	world.Reset()

	if orders := world.ListOrders("", "", 0); len(orders) != 0 {
		t.Errorf("ListOrders after Reset = %#v, want empty", orders)
	}
	if riders := world.ListRiders("", nil); len(riders) != 6 {
		t.Errorf("ListRiders after Reset has %d riders, want 6", len(riders))
	}
}

func TestWorld_DeterministicIDs(t *testing.T) {
	world := NewWorld()
	o1 := world.CreateOrder("c1", "STANDARD", LatLng{}, LatLng{})
	o2 := world.CreateOrder("c2", "STANDARD", LatLng{}, LatLng{})
	checkEqual(t, "o1.OrderID", o1.OrderID, "ord_0001")
	checkEqual(t, "o2.OrderID", o2.OrderID, "ord_0002")

	a1, errObj := world.Allocate(o1.OrderID)
	if errObj != nil {
		t.Fatalf("Allocate: unexpected error %+v", errObj)
	}
	checkEqual(t, "a1.AllocationID", a1.AllocationID, "alloc_0001")
}
