package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/errs"
)

// Remote is an HTTP client implementation of engine.Engine, talking to the
// server in internal/server (PLAN §22).
type Remote struct {
	mu     sync.RWMutex
	base   *url.URL // scheme + host only; Path/RawQuery/Fragment are always overwritten per request
	token  string
	client *http.Client

	// resolver, when set, lets do() recover from a connection-level
	// failure or a stale token by re-resolving the daemon's current
	// endpoint and retrying once (see EndpointResolver).
	resolver EndpointResolver

	ws *domain.Workspace // fetched once, in New, and cached for Workspace()

	// wsDir, when set, is sent as X-Sapien-Workspace on every request, so
	// one daemon serving several workspaces knows which one this client
	// means. Empty means "the daemon's primary workspace", which is what
	// every client sent before the daemon could hold more than one.
	wsDir string
}

// Option configures a Remote.
type Option func(*Remote)

// WithHTTPClient overrides the default http.Client (http.DefaultClient)
// used for every request, including the WebSocket handshake for
// Events().Subscribe.
func WithHTTPClient(c *http.Client) Option {
	return func(r *Remote) { r.client = c }
}

// WithWorkspace binds this client to one workspace of a daemon that serves
// several: every request carries server.WorkspaceHeader naming dir, and
// Workspace() reports that workspace rather than the daemon's primary one.
func WithWorkspace(dir string) Option {
	return func(r *Remote) { r.wsDir = dir }
}

// EndpointResolver returns the daemon endpoint (base URL and bearer token)
// that Remote should now be talking to. It is called by do() at most once
// per request, when the request fails in a way that suggests the daemon
// Remote was constructed against is gone or has rotated its token: a
// connection-level error (dial refused, EOF, connection reset, or similar)
// or an HTTP 401. It is not called for a context cancellation/deadline, or
// for any other error.
type EndpointResolver func(ctx context.Context) (baseURL, token string, err error)

// WithEndpointResolver installs resolver so Remote transparently
// reconnects when the daemon behind it is replaced -- by `sapien serve
// --restart`, or by the idle timeout recycling it -- instead of every
// in-flight client failing for the rest of the process's life (PLAN §4).
// See EndpointResolver for exactly when it is consulted.
func WithEndpointResolver(resolver EndpointResolver) Option {
	return func(r *Remote) { r.resolver = resolver }
}

// New connects to the daemon at baseURL (e.g. "http://127.0.0.1:51823",
// with no "/v1" suffix -- Remote appends the API's "/v1" prefix itself) and
// fetches its workspace once, caching it for Workspace(). token is sent as
// "Authorization: Bearer <token>" on every request.
func New(baseURL, token string, opts ...Option) (*Remote, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, errs.Wrap(errs.Invalid, err, "parsing base URL %q", baseURL)
	}
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""

	r := &Remote{base: u, token: token, client: http.DefaultClient}
	for _, opt := range opts {
		opt(r)
	}

	var ws domain.Workspace
	if err := r.do(context.Background(), http.MethodGet, "/v1/workspace", nil, nil, &ws); err != nil {
		return nil, err
	}
	r.ws = &ws
	return r, nil
}

// Workspace returns the workspace fetched at construction time.
func (r *Remote) Workspace() *domain.Workspace { return r.ws }

// Close closes idle HTTP connections. It does not affect in-flight requests
// or open Events().Subscribe WebSockets, which are governed by their own
// contexts.
func (r *Remote) Close() error {
	r.client.CloseIdleConnections()
	return nil
}

func (r *Remote) Services() engine.ServiceAPI { return (*serviceAPI)(r) }
func (r *Remote) Catalog() engine.CatalogAPI  { return (*catalogAPI)(r) }
func (r *Remote) Search() engine.SearchAPI    { return (*searchAPI)(r) }
func (r *Remote) Flows() engine.FlowAPI       { return (*flowAPI)(r) }
func (r *Remote) Runner() engine.RunnerAPI    { return (*runnerAPI)(r) }
func (r *Remote) Runs() engine.RunAPI         { return (*runAPI)(r) }
func (r *Remote) Memories() engine.MemoryAPI  { return (*memoryAPI)(r) }
func (r *Remote) Examples() engine.ExampleAPI { return (*exampleAPI)(r) }
func (r *Remote) Context() engine.ContextAPI  { return (*contextAPI)(r) }
func (r *Remote) Envs() engine.EnvAPI         { return (*envAPI)(r) }
func (r *Remote) Events() engine.EventAPI     { return (*eventAPI)(r) }

var _ engine.Engine = (*Remote)(nil)

// serviceAPI, catalogAPI, ... are all views of *Remote, mirroring the
// pattern in internal/engine/enginetest: each group gets direct access to
// the shared client/base/token without a second indirection.
type (
	serviceAPI Remote
	catalogAPI Remote
	searchAPI  Remote
	flowAPI    Remote
	runnerAPI  Remote
	runAPI     Remote
	memoryAPI  Remote
	contextAPI Remote
	envAPI     Remote
	eventAPI   Remote
)

func (s *serviceAPI) r() *Remote { return (*Remote)(s) }
func (c *catalogAPI) r() *Remote { return (*Remote)(c) }
func (s *searchAPI) r() *Remote  { return (*Remote)(s) }
func (fl *flowAPI) r() *Remote   { return (*Remote)(fl) }
func (r *runnerAPI) r() *Remote  { return (*Remote)(r) }
func (r *runAPI) r() *Remote     { return (*Remote)(r) }
func (m *memoryAPI) r() *Remote  { return (*Remote)(m) }
func (c *contextAPI) r() *Remote { return (*Remote)(c) }
func (e *envAPI) r() *Remote     { return (*Remote)(e) }
func (e *eventAPI) r() *Remote   { return (*Remote)(e) }

// do issues one request against the daemon: method/path (path is the
// decoded form, e.g. "/v1/docs/svc/docs/a.md#frag" -- url.URL escapes it
// correctly when the request is built, including "#" and other reserved
// characters), optional query, optional JSON body, and, on a 2xx response
// with a non-empty body, decodes the response into out (which may be nil).
// Non-2xx responses are decoded as errs JSON and returned as *errs.Error.
//
// When a resolver is configured (WithEndpointResolver), a connection-level
// failure or an HTTP 401 triggers exactly one reconnect-and-retry: do()
// calls the resolver, swaps Remote's base URL and token under r.mu, and
// replays the request once with the new endpoint. A request body is
// buffered up front so the replay sends the same bytes.
func (r *Remote) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var bodyBytes []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return errs.Wrap(errs.Internal, err, "encoding request body for %s %s", method, path)
		}
		bodyBytes = b
	}

	res := r.attempt(ctx, method, path, query, bodyBytes)
	if r.retryable(ctx, res) {
		if rerr := r.reconnect(ctx); rerr != nil {
			e := errs.As(r.toError(method, path, res))
			e.WithDetail("reconnect_error", rerr.Error())
			e.WithHint(fmt.Sprintf("re-resolving the daemon endpoint also failed: %v", rerr))
			return e
		}
		res = r.attempt(ctx, method, path, query, bodyBytes)
	}

	if err := r.toError(method, path, res); err != nil {
		return err
	}
	if out == nil || len(res.data) == 0 {
		return nil
	}
	if err := json.Unmarshal(res.data, out); err != nil {
		return errs.Wrap(errs.Internal, err, "decoding response body for %s %s", method, path)
	}
	return nil
}

// attemptResult is the outcome of one HTTP round trip: either a completed
// response (status/data, err nil) or a failure to complete one (err set).
// buildErr distinguishes "couldn't even build the request" (never
// retryable, always an internal error) from a genuine transport failure.
type attemptResult struct {
	status   int
	data     []byte
	err      error
	buildErr bool
}

// attempt performs one HTTP round trip against Remote's current base URL
// and token (read under r.mu, so a concurrent reconnect() is safe).
func (r *Remote) attempt(ctx context.Context, method, path string, query url.Values, bodyBytes []byte) attemptResult {
	u := r.currentBase()
	u.Path = path
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}

	var reqBody io.Reader
	if bodyBytes != nil {
		reqBody = bytes.NewReader(bodyBytes)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return attemptResult{err: err, buildErr: true}
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+r.currentToken())
	if dir := r.currentWorkspaceDir(); dir != "" {
		req.Header.Set(workspaceHeader, dir)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return attemptResult{err: err}
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return attemptResult{status: resp.StatusCode, err: err}
	}
	return attemptResult{status: resp.StatusCode, data: data}
}

// toError converts an attempt's outcome into the error do() returns: a
// transport failure becomes errs.HTTPTransport (or errs.Internal for a
// request that couldn't be built), a non-2xx response is decoded as errs
// JSON, and a clean 2xx response is nil.
func (r *Remote) toError(method, path string, res attemptResult) error {
	if res.err != nil {
		if res.buildErr {
			return errs.Wrap(errs.Internal, res.err, "building request for %s %s", method, path)
		}
		return errs.Wrap(errs.HTTPTransport, res.err, "%s %s", method, path)
	}
	if res.status >= http.StatusBadRequest {
		return decodeError(res.status, res.data)
	}
	return nil
}

// retryable reports whether res is worth one reconnect-and-retry: no
// resolver configured, a request that never left this process, and a
// cancelled/expired context all disqualify it. Otherwise it's a
// connection-level error (isConnError) or an HTTP 401 (a token the daemon
// no longer accepts, typically because it restarted).
func (r *Remote) retryable(ctx context.Context, res attemptResult) bool {
	if r.resolver == nil || ctx.Err() != nil || res.buildErr {
		return false
	}
	if res.err != nil {
		return isConnError(res.err)
	}
	return res.status == http.StatusUnauthorized
}

// isConnError reports whether err looks like the daemon is simply gone
// (dial refused, connection reset, EOF, or any other net.Error), as
// opposed to a context cancellation/deadline, which must never be
// retried.
func isConnError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// reconnect asks r.resolver for the daemon's current endpoint and, on
// success, swaps Remote's base URL and token under r.mu -- so every
// subsequent request, not just the one being retried, uses it.
func (r *Remote) reconnect(ctx context.Context) error {
	baseURL, token, err := r.resolver(ctx)
	if err != nil {
		return err
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return errs.Wrap(errs.Invalid, err, "parsing resolved base URL %q", baseURL)
	}
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""

	r.mu.Lock()
	r.base = u
	r.token = token
	r.mu.Unlock()
	return nil
}

// currentBase returns a copy of Remote's current base URL under r.mu.
func (r *Remote) currentBase() url.URL {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return *r.base
}

// currentToken returns Remote's current bearer token under r.mu.
func (r *Remote) currentToken() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.token
}

// decodeError turns a non-2xx response body (errs JSON, per httputil.go's
// writeError/writeErrorStatus) back into an *errs.Error carrying the
// original Code, Message, Details, and Hint. A body that isn't valid errs
// JSON (a proxy's HTML error page, say) becomes an E_INTERNAL error instead
// of a decode failure.
func decodeError(status int, body []byte) error {
	var e errs.Error
	if err := json.Unmarshal(body, &e); err != nil || e.Code == "" {
		return errs.New(errs.Internal, "http %d: %s", status, strings.TrimSpace(string(body)))
	}
	return &e
}

// workspaceHeader mirrors server.WorkspaceHeader. It is duplicated rather
// than imported because internal/server imports this package's sibling
// engine interfaces, and a constant is cheaper than the dependency.
const workspaceHeader = "X-Sapien-Workspace"

// currentWorkspaceDir reads wsDir under the same lock that guards base and
// token, so a concurrent reconnect() is safe.
func (r *Remote) currentWorkspaceDir() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.wsDir
}
