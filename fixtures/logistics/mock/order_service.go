package mock

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// NewOrderService returns an http.Handler implementing order-service's API
// (createOrder, listOrders, getOrder, cancelOrder, getOrderTimeline; see
// fixtures/logistics/order-service/api/openapi.yaml) backed by opts.World.
func NewOrderService(opts Options) http.Handler {
	world := opts.World
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/orders", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			CustomerID string `json:"customerId"`
			Type       string `json:"type"`
			Pickup     LatLng `json:"pickup"`
			Drop       LatLng `json:"drop"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, &Error{Code: CodeInvalidRequest, Message: "malformed JSON body"})
			return
		}
		if req.CustomerID == "" || (req.Type != "STANDARD" && req.Type != "QCOM") {
			writeError(w, &Error{Code: CodeInvalidRequest, Message: "customerId and type (STANDARD|QCOM) are required"})
			return
		}
		order := world.CreateOrder(req.CustomerID, req.Type, req.Pickup, req.Drop)
		writeJSON(w, http.StatusCreated, order)
	})

	mux.HandleFunc("GET /v1/orders", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := 50
		if v := q.Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		orders := world.ListOrders(q.Get("customerId"), q.Get("status"), limit)
		writeJSON(w, http.StatusOK, map[string]any{"orders": orders})
	})

	mux.HandleFunc("GET /v1/orders/{orderId}", func(w http.ResponseWriter, r *http.Request) {
		order, errObj := world.GetOrder(r.PathValue("orderId"))
		if errObj != nil {
			writeError(w, errObj)
			return
		}
		writeJSON(w, http.StatusOK, order)
	})

	mux.HandleFunc("POST /v1/orders/{orderId}/cancel", func(w http.ResponseWriter, r *http.Request) {
		order, errObj := world.CancelOrder(r.PathValue("orderId"))
		if errObj != nil {
			writeError(w, errObj)
			return
		}
		writeJSON(w, http.StatusOK, order)
	})

	mux.HandleFunc("GET /v1/orders/{orderId}/timeline", func(w http.ResponseWriter, r *http.Request) {
		tl, errObj := world.GetOrderTimeline(r.PathValue("orderId"))
		if errObj != nil {
			writeError(w, errObj)
			return
		}
		writeJSON(w, http.StatusOK, tl)
	})

	return withOptionalAuth(mux, opts)
}
