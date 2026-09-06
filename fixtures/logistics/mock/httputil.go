package mock

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// writeJSON writes v as a JSON response body with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("mock: encode response: %v", err)
	}
}

// statusForCode maps an Error.Code to the HTTP status the fixture services
// use for it. Centralizing this keeps the mapping consistent across all
// three services.
func statusForCode(code string) int {
	switch code {
	case CodeOrderNotFound, CodeAllocationNotFound, CodeRiderNotFound, CodeTimelineNotReady:
		return http.StatusNotFound
	case CodeNoRiderAvailable, CodeOrderAlreadyDelivered, CodeAllocationAlreadyReleased:
		return http.StatusConflict
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeInvalidRequest:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// writeError writes e as a JSON error body with the status statusForCode(e.Code) maps to.
func writeError(w http.ResponseWriter, e *Error) {
	writeJSON(w, statusForCode(e.Code), e)
}

// requireAuth wraps next so that requests without a well-formed
// "Authorization: Bearer <token>" header are rejected with 401
// CodeUnauthorized before reaching next.
func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") == "" {
			writeError(w, &Error{Code: CodeUnauthorized, Message: "missing or invalid Authorization header"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withOptionalAuth returns h wrapped in requireAuth when opts.RequireAuth is set.
func withOptionalAuth(h http.Handler, opts Options) http.Handler {
	if opts.RequireAuth {
		return requireAuth(h)
	}
	return h
}
