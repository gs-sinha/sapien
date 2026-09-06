package runtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/gs-sinha/sapien/internal/errs"
)

const (
	defaultConnectTimeout = 10 * time.Second
	defaultTotalTimeout   = 30 * time.Second
	defaultMaxRedirects   = 5
	defaultMaxBodyBytes   = int64(1 << 20) // 1 MiB
	defaultUserAgent      = "sapien/dev"
)

// Client executes Requests over HTTP using a dedicated http.Transport built
// from Options.
type Client struct {
	http         *http.Client
	opts         Options
	totalTimeout time.Duration
	maxBodyBytes int64
}

// New builds a Client with a dedicated transport: no shared/global state, no
// implicit environment-proxy inheritance (unless Transport.Proxy == "env"),
// and TLS verification skipped only when both InsecureTLS is set and
// Production is false.
func New(opts Options) *Client {
	connectTimeout := defaultConnectTimeout
	if opts.Transport.ConnectTimeoutMs > 0 {
		connectTimeout = time.Duration(opts.Transport.ConnectTimeoutMs) * time.Millisecond
	}
	totalTimeout := defaultTotalTimeout
	if opts.Transport.TimeoutMs > 0 {
		totalTimeout = time.Duration(opts.Transport.TimeoutMs) * time.Millisecond
	}
	maxRedirects := defaultMaxRedirects
	if opts.Transport.MaxRedirects > 0 {
		maxRedirects = opts.Transport.MaxRedirects
	}
	maxBodyBytes := defaultMaxBodyBytes
	if opts.Transport.MaxBodyBytes > 0 {
		maxBodyBytes = opts.Transport.MaxBodyBytes
	}

	tlsConfig := &tls.Config{}
	if opts.Transport.InsecureTLS && !opts.Production {
		tlsConfig.InsecureSkipVerify = true
	}

	dialer := &net.Dialer{Timeout: connectTimeout}

	transport := &http.Transport{
		Proxy:                 proxyFunc(opts.Transport.Proxy),
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   connectTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       tlsConfig,
	}

	httpClient := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			// Never forward Authorization to a different host, even a
			// same-machine, different-port one (Go's own stdlib stripping
			// compares hostnames only, which is not strict enough here).
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				req.Header.Del("Authorization")
				req.Header.Del("Proxy-Authorization")
			}
			return nil
		},
	}

	return &Client{
		http:         httpClient,
		opts:         opts,
		totalTimeout: totalTimeout,
		maxBodyBytes: maxBodyBytes,
	}
}

func proxyFunc(proxy string) func(*http.Request) (*url.URL, error) {
	switch proxy {
	case "":
		return nil
	case "env":
		return http.ProxyFromEnvironment
	default:
		u, err := url.Parse(proxy)
		if err != nil {
			return nil
		}
		return http.ProxyURL(u)
	}
}

// Do sends req and returns the response. Non-2xx status codes are not
// errors. ctx cancellation/deadline surfaces as errs.Cancelled; a per-request
// timeout firing, or a network/TLS/DNS failure, surfaces as
// errs.HTTPTransport.
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	if req.Method == "" {
		req.Method = http.MethodGet
	}

	bodyBytes, isJSONBody, err := encodeBody(req.Body)
	if err != nil {
		return nil, errs.Wrap(errs.Invalid, err, "failed to encode request body").WithDetail("url", req.URL)
	}

	contentType := req.ContentType
	if contentType == "" && isJSONBody {
		contentType = "application/json"
	}
	req.ContentType = contentType

	headers := cloneCanonicalHeaders(req.Headers)
	if contentType != "" && headers[http.CanonicalHeaderKey("Content-Type")] == "" {
		headers[http.CanonicalHeaderKey("Content-Type")] = contentType
	}
	ua := c.opts.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	if headers[http.CanonicalHeaderKey("User-Agent")] == "" {
		headers[http.CanonicalHeaderKey("User-Agent")] = ua
	}
	req.Headers = headers

	timeout := c.totalTimeout
	if req.Timeout > 0 {
		timeout = req.Timeout
	}
	reqCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var bodyReader io.Reader
	if bodyBytes != nil {
		bodyReader = bytes.NewReader(bodyBytes)
	}

	httpReq, err := http.NewRequestWithContext(reqCtx, req.Method, req.URL, bodyReader)
	if err != nil {
		return nil, errs.Wrap(errs.HTTPTransport, err, "invalid request to %s", req.URL).WithDetail("url", req.URL).WithDetail("cause", err.Error())
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	timer := &traceTimer{}
	reqCtx = httptrace.WithClientTrace(reqCtx, timer.clientTrace())
	httpReq = httpReq.WithContext(reqCtx)

	start := time.Now()
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, c.classify(err, ctx, reqCtx, req, timeout)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, c.maxBodyBytes+1)
	data, readErr := io.ReadAll(limited)
	total := time.Since(start)
	if readErr != nil {
		return nil, c.classify(readErr, ctx, reqCtx, req, timeout)
	}

	size := int64(len(data))
	truncated := size > c.maxBodyBytes
	bodyRaw := data
	if truncated {
		bodyRaw = data[:c.maxBodyBytes]
	}

	var parsedBody any
	if !truncated {
		if v, ok := ParseJSONBody(bodyRaw); ok {
			parsedBody = v
		}
	}

	return &Response{
		Status:    resp.StatusCode,
		Headers:   joinHeaders(resp.Header),
		Body:      parsedBody,
		BodyRaw:   bodyRaw,
		Truncated: truncated,
		Size:      size,
		Timings:   timer.timings(start, total),
		Request:   req,
	}, nil
}

// classify turns a transport-level error into a structured errs.Error. It
// distinguishes the caller's own context being cancelled/expired (Cancelled)
// from our own per-request timeout firing, or a genuine network/TLS/DNS
// failure (both HTTPTransport).
func (c *Client) classify(err error, ctx, reqCtx context.Context, req Request, timeout time.Duration) error {
	if ctx.Err() != nil {
		return errs.Wrap(errs.Cancelled, ctx.Err(), "request to %s cancelled", req.URL).WithDetail("url", req.URL)
	}
	if reqCtx.Err() == context.DeadlineExceeded {
		return errs.Wrap(errs.HTTPTransport, err, "request to %s timed out", req.URL).
			WithDetail("url", req.URL).
			WithDetail("cause", err.Error()).
			WithHint(fmt.Sprintf("timed out after %gs", timeout.Seconds()))
	}
	return classifyNetworkError(err, req)
}

func classifyNetworkError(err error, req Request) error {
	e := errs.Wrap(errs.HTTPTransport, err, "request to %s failed", req.URL).
		WithDetail("url", req.URL).
		WithDetail("cause", err.Error())

	switch {
	case isConnRefused(err):
		host := hostOf(req.URL)
		e = e.WithHint(fmt.Sprintf("is the service running at %s?", host))
	case isTLSError(err):
		e = e.WithHint("set transport.insecure_tls: true for local self-signed certs (non-production only)")
	}
	return e
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	return u.Host
}

func isConnRefused(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		var sysErr *os.SyscallError
		if errors.As(opErr.Err, &sysErr) {
			return errors.Is(sysErr.Err, syscall.ECONNREFUSED)
		}
		return errors.Is(opErr.Err, syscall.ECONNREFUSED)
	}
	return strings.Contains(err.Error(), "connection refused")
}

func isTLSError(err error) bool {
	var unknownAuth x509.UnknownAuthorityError
	if errors.As(err, &unknownAuth) {
		return true
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return true
	}
	var certInvalid x509.CertificateInvalidError
	if errors.As(err, &certInvalid) {
		return true
	}
	var recordErr tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "tls") || strings.Contains(msg, "certificate") || strings.Contains(msg, "x509")
}

func cloneCanonicalHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[http.CanonicalHeaderKey(k)] = v
	}
	return out
}

func joinHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = strings.Join(v, ", ")
	}
	return out
}
