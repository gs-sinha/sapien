package mock

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// NewRiderService returns an http.Handler implementing rider-service's API
// (getRider, searchRiders, updateRiderStatus, listRiders; see
// fixtures/logistics/rider-service/api/openapi.yaml) backed by opts.World.
func NewRiderService(opts Options) http.Handler {
	world := opts.World
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/riders", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var online *bool
		if v := q.Get("online"); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				online = &b
			}
		}
		riders := world.ListRiders(q.Get("city"), online)
		writeJSON(w, http.StatusOK, map[string]any{"riders": riders})
	})

	mux.HandleFunc("POST /v1/riders/search", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Lat      float64 `json:"lat"`
			Lng      float64 `json:"lng"`
			RadiusKm float64 `json:"radiusKm"`
			QcomOnly bool    `json:"qcomOnly"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, &Error{Code: CodeInvalidRequest, Message: "malformed JSON body"})
			return
		}
		riders := world.SearchRiders(req.QcomOnly)
		writeJSON(w, http.StatusOK, map[string]any{"riders": riders})
	})

	mux.HandleFunc("GET /v1/riders/{riderId}", func(w http.ResponseWriter, r *http.Request) {
		rider, errObj := world.GetRider(r.PathValue("riderId"))
		if errObj != nil {
			writeError(w, errObj)
			return
		}
		writeJSON(w, http.StatusOK, rider)
	})

	mux.HandleFunc("PATCH /v1/riders/{riderId}/status", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Online bool `json:"online"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, &Error{Code: CodeInvalidRequest, Message: "malformed JSON body"})
			return
		}
		rider, errObj := world.UpdateRiderStatus(r.PathValue("riderId"), req.Online)
		if errObj != nil {
			writeError(w, errObj)
			return
		}
		writeJSON(w, http.StatusOK, rider)
	})

	return withOptionalAuth(mux, opts)
}
