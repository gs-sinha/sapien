package mock

import (
	"encoding/json"
	"net/http"
)

// NewAllocationService returns an http.Handler implementing
// allocation-service's API (allocate, listAllocations, an operationId-less
// stats route, getAllocation, releaseAllocation, and the deprecated
// allocateV1; see
// fixtures/logistics/allocation-service/api/openapi.yaml) backed by
// opts.World.
func NewAllocationService(opts Options) http.Handler {
	world := opts.World
	mux := http.NewServeMux()

	allocateHandler := func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OrderID string `json:"orderId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrderID == "" {
			writeError(w, &Error{Code: CodeInvalidRequest, Message: "orderId is required"})
			return
		}
		alloc, errObj := world.Allocate(req.OrderID)
		if errObj != nil {
			writeError(w, errObj)
			return
		}
		writeJSON(w, http.StatusCreated, alloc)
	}

	mux.HandleFunc("POST /v1/allocations", allocateHandler)
	mux.HandleFunc("POST /v1/allocate", allocateHandler) // deprecated allocateV1 alias

	mux.HandleFunc("GET /v1/allocations", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		allocs := world.ListAllocations(q.Get("orderId"), q.Get("riderId"))
		writeJSON(w, http.StatusOK, map[string]any{"allocations": allocs})
	})

	mux.HandleFunc("GET /v1/allocations/stats", func(w http.ResponseWriter, r *http.Request) {
		total, allocated, released := world.AllocationStats()
		writeJSON(w, http.StatusOK, map[string]any{"total": total, "allocated": allocated, "released": released})
	})

	mux.HandleFunc("GET /v1/allocations/{allocationId}", func(w http.ResponseWriter, r *http.Request) {
		alloc, errObj := world.GetAllocation(r.PathValue("allocationId"))
		if errObj != nil {
			writeError(w, errObj)
			return
		}
		writeJSON(w, http.StatusOK, alloc)
	})

	mux.HandleFunc("POST /v1/allocations/{allocationId}/release", func(w http.ResponseWriter, r *http.Request) {
		alloc, errObj := world.ReleaseAllocation(r.PathValue("allocationId"))
		if errObj != nil {
			writeError(w, errObj)
			return
		}
		writeJSON(w, http.StatusOK, alloc)
	})

	return withOptionalAuth(mux, opts)
}
