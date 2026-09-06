package runtime

import (
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
)

// Request is a fully-formed HTTP request ready to send.
type Request struct {
	Method  string
	URL     string
	Headers map[string]string

	// Body is the request payload. nil means no body. A []byte or string is
	// sent as-is (verbatim bytes). Any other value is JSON-encoded.
	Body any

	// ContentType overrides the Content-Type header. When empty, it defaults
	// to "application/json" if Body was JSON-encoded, otherwise no
	// Content-Type header is added.
	ContentType string

	// Timeout overrides the transport's default total timeout for this
	// request. Zero means "use the transport default".
	Timeout time.Duration
}

// Response is the result of executing a Request.
type Response struct {
	Status int

	// Headers uses canonical header names; multiple values for the same
	// header are joined with ", ".
	Headers map[string]string

	// Body is the parsed JSON body (integers preserved as int64, other
	// numbers as float64) when BodyRaw is valid JSON and was not truncated.
	// Otherwise Body is nil.
	Body any

	// BodyRaw is the response body, capped at Options.Transport.MaxBodyBytes.
	BodyRaw []byte

	// Truncated is true when the response body exceeded the configured cap.
	Truncated bool

	// Size is the number of bytes actually read off the wire, which can be
	// up to MaxBodyBytes+1 (the extra byte is read only to detect
	// truncation and is not included in BodyRaw).
	Size int64

	Timings domain.Timings

	// Request is the request as actually sent, after defaults (method,
	// content type, user agent) were applied. Intended for records.
	Request Request
}

// Options configures a Client.
type Options struct {
	Transport domain.Transport

	// Production disables InsecureTLS regardless of Transport.InsecureTLS.
	Production bool

	// UserAgent is sent as the User-Agent header when the request does not
	// set one explicitly. Defaults to "sapien/dev" when empty.
	UserAgent string
}
