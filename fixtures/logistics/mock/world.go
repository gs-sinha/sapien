// Package mock implements in-memory HTTP mock servers for the three
// logistics fixture services (order-service, allocation-service,
// rider-service) described in fixtures/logistics/README.md. The three
// services share one in-memory World, so cross-service effects (allocating
// a rider, cancelling an order) are visible immediately to every service,
// the same way they would be through a real database in production.
package mock

import (
	"fmt"
	"sync"
	"time"
)

// LatLng is a geographic coordinate used by an order's pickup/drop points.
type LatLng struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// TimelineEvent is one entry in an order's delivery timeline.
type TimelineEvent struct {
	Status string `json:"status"`
	At     string `json:"at"`
	Note   string `json:"note,omitempty"`
}

// Order is the public JSON representation of an order.
type Order struct {
	OrderID    string          `json:"orderId"`
	Status     string          `json:"status"`
	Type       string          `json:"type"`
	CustomerID string          `json:"customerId"`
	CreatedAt  string          `json:"createdAt"`
	Timeline   []TimelineEvent `json:"timeline,omitempty"`
}

// Timeline is the public JSON representation returned by getOrderTimeline.
type Timeline struct {
	OrderID    string          `json:"orderId"`
	Events     []TimelineEvent `json:"events"`
	EtaMinutes int             `json:"etaMinutes"`
}

// Allocation is the public JSON representation of an allocation.
type Allocation struct {
	AllocationID string `json:"allocationId"`
	OrderID      string `json:"orderId"`
	RiderID      string `json:"riderId"`
	Status       string `json:"status"`
	AllocatedAt  string `json:"allocatedAt"`
}

// Rider is the public JSON representation of a rider.
type Rider struct {
	RiderID       string   `json:"riderId"`
	Name          string   `json:"name"`
	Online        bool     `json:"online"`
	QcomSkill     bool     `json:"qcomSkill"`
	UpcomingTrips int      `json:"upcomingTrips"`
	City          string   `json:"city"`
	Rating        *float64 `json:"rating"`
}

// Error is the shared {code, message} error shape used by all three
// services, matching the Error schema in every service's openapi.yaml.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error-code constants used by World methods below.
const (
	CodeOrderNotFound             = "ORDER_NOT_FOUND"
	CodeOrderAlreadyDelivered     = "ORDER_ALREADY_DELIVERED"
	CodeTimelineNotReady          = "TIMELINE_NOT_READY"
	CodeAllocationNotFound        = "ALLOCATION_NOT_FOUND"
	CodeAllocationAlreadyReleased = "ALLOCATION_ALREADY_RELEASED"
	CodeNoRiderAvailable          = "NO_RIDER_AVAILABLE"
	CodeRiderNotFound             = "RIDER_NOT_FOUND"
	CodeInvalidRequest            = "INVALID_REQUEST"
	CodeUnauthorized              = "UNAUTHORIZED"
)

// defaultTimelineDelay is how long getOrderTimeline returns 404
// TIMELINE_NOT_READY after an order is created, by default.
const defaultTimelineDelay = 1500 * time.Millisecond

// internal mutable records; DTOs above are derived from these under lock.

type orderRecord struct {
	orderID      string
	status       string
	typ          string
	customerID   string
	pickup       LatLng
	drop         LatLng
	createdAt    time.Time
	allocationID string
	events       []TimelineEvent
}

type allocationRecord struct {
	allocationID string
	orderID      string
	riderID      string
	status       string
	allocatedAt  time.Time
}

type riderRecord struct {
	riderID       string
	name          string
	online        bool
	qcomSkill     bool
	upcomingTrips int
	city          string
	rating        *float64
	busy          bool // currently assigned to an un-released allocation
}

// World is the in-memory state shared by the order, allocation, and rider
// mock services. All access goes through World's methods, which are
// goroutine-safe.
type World struct {
	mu sync.Mutex

	riders      map[string]*riderRecord
	orders      map[string]*orderRecord
	allocations map[string]*allocationRecord

	// insertion-order slices so list endpoints are deterministic.
	riderOrder      []string
	orderOrder      []string
	allocationOrder []string

	nextOrderSeq      int
	nextAllocationSeq int

	timelineDelay time.Duration
	now           func() time.Time
}

func ratingPtr(v float64) *float64 { return &v }

// NewWorld creates a freshly seeded World: six riders across two cities
// (Bangalore, Mumbai), a mix of online/offline and qcomSkill true/false,
// including R123 (online, qcomSkill=true) and R124 (online,
// qcomSkill=false).
func NewWorld() *World {
	w := &World{now: time.Now}
	w.Reset()
	return w
}

// Reset reinitializes the world to its seed state: riders are reseeded to
// the default table, orders and allocations are cleared, and ID counters
// restart from zero. Useful between test cases that share a World.
func (w *World) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.orders = make(map[string]*orderRecord)
	w.allocations = make(map[string]*allocationRecord)
	w.orderOrder = nil
	w.allocationOrder = nil
	w.nextOrderSeq = 0
	w.nextAllocationSeq = 0
	w.timelineDelay = defaultTimelineDelay

	w.riders = map[string]*riderRecord{
		"R123": {riderID: "R123", name: "Asha Rao", online: true, qcomSkill: true, upcomingTrips: 1, city: "Bangalore", rating: ratingPtr(4.8)},
		"R124": {riderID: "R124", name: "Farhan Sheikh", online: true, qcomSkill: false, upcomingTrips: 0, city: "Bangalore", rating: ratingPtr(4.5)},
		"R125": {riderID: "R125", name: "Divya Nair", online: false, qcomSkill: true, upcomingTrips: 2, city: "Bangalore", rating: ratingPtr(4.9)},
		"R126": {riderID: "R126", name: "Karan Mehta", online: true, qcomSkill: true, upcomingTrips: 0, city: "Mumbai", rating: nil},
		"R127": {riderID: "R127", name: "Sana Iyer", online: false, qcomSkill: false, upcomingTrips: 0, city: "Mumbai", rating: ratingPtr(4.2)},
		"R128": {riderID: "R128", name: "Vikram Singh", online: true, qcomSkill: false, upcomingTrips: 3, city: "Mumbai", rating: ratingPtr(4.6)},
	}
	w.riderOrder = []string{"R123", "R124", "R125", "R126", "R127", "R128"}
}

// SetTimelineDelay overrides how long getOrderTimeline waits after order
// creation before it returns 200 instead of 404 TIMELINE_NOT_READY. Tests
// typically set this to 0. The default (set by NewWorld/Reset) is 1500ms.
func (w *World) SetTimelineDelay(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timelineDelay = d
}

func cloneEvents(events []TimelineEvent) []TimelineEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]TimelineEvent, len(events))
	copy(out, events)
	return out
}

func (o *orderRecord) toDTO() Order {
	return Order{
		OrderID:    o.orderID,
		Status:     o.status,
		Type:       o.typ,
		CustomerID: o.customerID,
		CreatedAt:  o.createdAt.UTC().Format(time.RFC3339),
		Timeline:   cloneEvents(o.events),
	}
}

func (a *allocationRecord) toDTO() Allocation {
	return Allocation{
		AllocationID: a.allocationID,
		OrderID:      a.orderID,
		RiderID:      a.riderID,
		Status:       a.status,
		AllocatedAt:  a.allocatedAt.UTC().Format(time.RFC3339),
	}
}

func (r *riderRecord) toDTO() Rider {
	return Rider{
		RiderID:       r.riderID,
		Name:          r.name,
		Online:        r.online,
		QcomSkill:     r.qcomSkill,
		UpcomingTrips: r.upcomingTrips,
		City:          r.city,
		Rating:        r.rating,
	}
}

// --- orders ---------------------------------------------------------------

// CreateOrder creates a new order in CREATED status with a single CREATED
// timeline event, and returns its DTO. IDs are deterministic: ord_0001,
// ord_0002, ...
func (w *World) CreateOrder(customerID, typ string, pickup, drop LatLng) Order {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.nextOrderSeq++
	id := fmt.Sprintf("ord_%04d", w.nextOrderSeq)
	now := w.now()
	rec := &orderRecord{
		orderID:    id,
		status:     "CREATED",
		typ:        typ,
		customerID: customerID,
		pickup:     pickup,
		drop:       drop,
		createdAt:  now,
	}
	rec.events = append(rec.events, TimelineEvent{Status: "CREATED", At: now.UTC().Format(time.RFC3339)})
	w.orders[id] = rec
	w.orderOrder = append(w.orderOrder, id)
	return rec.toDTO()
}

// GetOrder returns the order with the given ID, or CodeOrderNotFound.
func (w *World) GetOrder(id string) (Order, *Error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec, ok := w.orders[id]
	if !ok {
		return Order{}, &Error{Code: CodeOrderNotFound, Message: fmt.Sprintf("no order with id %q", id)}
	}
	return rec.toDTO(), nil
}

// ListOrders returns orders in creation order, optionally filtered by
// customerID and/or status, capped at limit results (limit <= 0 means no cap).
func (w *World) ListOrders(customerID, status string, limit int) []Order {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Order, 0)
	for _, id := range w.orderOrder {
		rec := w.orders[id]
		if customerID != "" && rec.customerID != customerID {
			continue
		}
		if status != "" && rec.status != status {
			continue
		}
		out = append(out, rec.toDTO())
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// CancelOrder cancels an order and releases any allocation attached to it.
// Returns CodeOrderNotFound or CodeOrderAlreadyDelivered on failure.
func (w *World) CancelOrder(id string) (Order, *Error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	order, ok := w.orders[id]
	if !ok {
		return Order{}, &Error{Code: CodeOrderNotFound, Message: fmt.Sprintf("no order with id %q", id)}
	}
	if order.status == "DELIVERED" {
		return Order{}, &Error{Code: CodeOrderAlreadyDelivered, Message: fmt.Sprintf("order %q has already been delivered", id)}
	}
	if order.allocationID != "" {
		// Best effort: ignore an already-released allocation.
		_, _ = w.releaseAllocationLocked(order.allocationID)
	}
	order.status = "CANCELLED"
	now := w.now()
	order.events = append(order.events, TimelineEvent{Status: "CANCELLED", At: now.UTC().Format(time.RFC3339)})
	return order.toDTO(), nil
}

// GetOrderTimeline returns the timeline for an order. Returns
// CodeTimelineNotReady if called within TimelineDelay of order creation.
func (w *World) GetOrderTimeline(id string) (Timeline, *Error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	order, ok := w.orders[id]
	if !ok {
		return Timeline{}, &Error{Code: CodeOrderNotFound, Message: fmt.Sprintf("no order with id %q", id)}
	}
	if w.now().Sub(order.createdAt) < w.timelineDelay {
		return Timeline{}, &Error{Code: CodeTimelineNotReady, Message: fmt.Sprintf("timeline for %q is not ready yet", id)}
	}

	eta := 30
	switch order.status {
	case "ALLOCATED":
		eta = 20
	case "CANCELLED", "DELIVERED":
		eta = 0
	}
	return Timeline{OrderID: id, Events: cloneEvents(order.events), EtaMinutes: eta}, nil
}

// --- allocations ------------------------------------------------------------

// Allocate picks an eligible, online rider for orderID and creates an
// allocation. QCOM orders require a rider with qcomSkill=true. Returns
// CodeOrderNotFound or CodeNoRiderAvailable on failure.
func (w *World) Allocate(orderID string) (Allocation, *Error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	order, ok := w.orders[orderID]
	if !ok {
		return Allocation{}, &Error{Code: CodeOrderNotFound, Message: fmt.Sprintf("no order with id %q", orderID)}
	}

	var chosen *riderRecord
	for _, rid := range w.riderOrder {
		r := w.riders[rid]
		if !r.online || r.busy {
			continue
		}
		if order.typ == "QCOM" && !r.qcomSkill {
			continue
		}
		chosen = r
		break
	}
	if chosen == nil {
		return Allocation{}, &Error{Code: CodeNoRiderAvailable, Message: fmt.Sprintf("no eligible rider is online for order %q", orderID)}
	}

	w.nextAllocationSeq++
	id := fmt.Sprintf("alloc_%04d", w.nextAllocationSeq)
	now := w.now()
	rec := &allocationRecord{allocationID: id, orderID: orderID, riderID: chosen.riderID, status: "ALLOCATED", allocatedAt: now}
	w.allocations[id] = rec
	w.allocationOrder = append(w.allocationOrder, id)
	chosen.busy = true

	order.status = "ALLOCATED"
	order.allocationID = id
	order.events = append(order.events, TimelineEvent{
		Status: "ALLOCATED",
		At:     now.UTC().Format(time.RFC3339),
		Note:   "allocated to " + chosen.riderID,
	})

	return rec.toDTO(), nil
}

// GetAllocation returns the allocation with the given ID, or CodeAllocationNotFound.
func (w *World) GetAllocation(id string) (Allocation, *Error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec, ok := w.allocations[id]
	if !ok {
		return Allocation{}, &Error{Code: CodeAllocationNotFound, Message: fmt.Sprintf("no allocation with id %q", id)}
	}
	return rec.toDTO(), nil
}

// ListAllocations returns allocations in creation order, optionally
// filtered by orderID and/or riderID.
func (w *World) ListAllocations(orderID, riderID string) []Allocation {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Allocation, 0)
	for _, id := range w.allocationOrder {
		rec := w.allocations[id]
		if orderID != "" && rec.orderID != orderID {
			continue
		}
		if riderID != "" && rec.riderID != riderID {
			continue
		}
		out = append(out, rec.toDTO())
	}
	return out
}

// ReleaseAllocation frees the rider assigned to an allocation. Returns
// CodeAllocationNotFound or CodeAllocationAlreadyReleased on failure.
func (w *World) ReleaseAllocation(id string) (Allocation, *Error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec, errObj := w.releaseAllocationLocked(id)
	if errObj != nil {
		return Allocation{}, errObj
	}
	return rec.toDTO(), nil
}

// releaseAllocationLocked assumes w.mu is already held.
func (w *World) releaseAllocationLocked(id string) (*allocationRecord, *Error) {
	rec, ok := w.allocations[id]
	if !ok {
		return nil, &Error{Code: CodeAllocationNotFound, Message: fmt.Sprintf("no allocation with id %q", id)}
	}
	if rec.status == "RELEASED" {
		return nil, &Error{Code: CodeAllocationAlreadyReleased, Message: fmt.Sprintf("allocation %q is already released", id)}
	}
	rec.status = "RELEASED"
	if r, ok := w.riders[rec.riderID]; ok {
		r.busy = false
	}
	return rec, nil
}

// AllocationStats returns aggregate allocation counts, backing the
// operationId-less GET /v1/allocations/stats route.
func (w *World) AllocationStats() (total, allocated, released int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, id := range w.allocationOrder {
		rec := w.allocations[id]
		total++
		switch rec.status {
		case "ALLOCATED":
			allocated++
		case "RELEASED":
			released++
		}
	}
	return total, allocated, released
}

// --- riders -----------------------------------------------------------------

// GetRider returns the rider with the given ID, or CodeRiderNotFound.
func (w *World) GetRider(id string) (Rider, *Error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	r, ok := w.riders[id]
	if !ok {
		return Rider{}, &Error{Code: CodeRiderNotFound, Message: fmt.Sprintf("no rider with id %q", id)}
	}
	return r.toDTO(), nil
}

// ListRiders returns riders, optionally filtered by city and/or online status.
func (w *World) ListRiders(city string, online *bool) []Rider {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Rider, 0)
	for _, id := range w.riderOrder {
		r := w.riders[id]
		if city != "" && r.city != city {
			continue
		}
		if online != nil && r.online != *online {
			continue
		}
		out = append(out, r.toDTO())
	}
	return out
}

// SearchRiders returns riders, ignoring lat/lng/radius (this is a fixture,
// not a geo engine) but honoring qcomOnly when set.
func (w *World) SearchRiders(qcomOnly bool) []Rider {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Rider, 0)
	for _, id := range w.riderOrder {
		r := w.riders[id]
		if qcomOnly && !r.qcomSkill {
			continue
		}
		out = append(out, r.toDTO())
	}
	return out
}

// UpdateRiderStatus sets a rider's online flag and returns the updated rider.
func (w *World) UpdateRiderStatus(id string, online bool) (Rider, *Error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	r, ok := w.riders[id]
	if !ok {
		return Rider{}, &Error{Code: CodeRiderNotFound, Message: fmt.Sprintf("no rider with id %q", id)}
	}
	r.online = online
	return r.toDTO(), nil
}
