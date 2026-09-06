package server

import (
	"encoding/json"
	"net/http"

	"github.com/growsimplee/sapien/internal/errs"
)

// writeJSON writes v as a JSON body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// writeNoContent writes an empty 204 response.
func writeNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// writeError serializes err as errs JSON with status derived from
// errs.HTTPStatus.
func writeError(w http.ResponseWriter, err error) {
	e := errs.As(err)
	writeErrorStatus(w, errs.HTTPStatus(e), e)
}

// writeErrorStatus serializes e as errs JSON with an explicit status,
// bypassing errs.HTTPStatus. Used by the auth/host/origin middleware, whose
// statuses (401/403) don't come from the generic error-code mapping, and by
// the 404 handler for unmatched routes.
func writeErrorStatus(w http.ResponseWriter, status int, e *errs.Error) {
	writeJSON(w, status, e)
}

// decodeJSON decodes r's JSON body into v, wrapping any failure as
// errs.Invalid. An empty body is treated as an empty object.
func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return errs.Wrap(errs.Invalid, err, "decoding request body")
	}
	return nil
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeErrorStatus(w, http.StatusNotFound, errs.New(errs.Invalid, "no route for %s %s", r.Method, r.URL.Path))
}

func (s *Server) handleMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeErrorStatus(w, http.StatusMethodNotAllowed, errs.New(errs.Invalid, "method %s not allowed for %s", r.Method, r.URL.Path))
}
