package server

import (
	"encoding/json"
	"net/http"

	"github.com/gs-sinha/sapien/internal/errs"
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

// flushResponse flushes w to the client immediately, if the underlying
// ResponseWriter supports it (http.NewResponseController's Unwrap
// convention finds the real Flusher through loggingMiddleware's
// statusRecorder wrapper -- see its own doc comment). Used before a handler
// goes on to do something that outlives the request, e.g. spawning a
// successor daemon (handleDaemonRestart, handleUpdateApply) that may kill
// this process: the client should see its response land before that
// happens, not race it. A ResponseWriter that cannot flush (e.g. an
// httptest.ResponseRecorder in a unit test) is a no-op, not an error.
func flushResponse(w http.ResponseWriter) {
	_ = http.NewResponseController(w).Flush()
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
