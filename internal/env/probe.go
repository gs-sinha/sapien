package env

import (
	"context"
	"crypto/tls"
	"net/http"
	"sort"
	"time"

	"github.com/growsimplee/sapien/internal/domain"
)

// ProbeResult is one service's reachability check, as produced by Probe.
type ProbeResult struct {
	Service string `json:"service"`
	BaseURL string `json:"base_url"`
	// Status is the HTTP status code received. It is 0 if no response was
	// received at all (see Error).
	Status int `json:"status,omitempty"`
	// Error is set instead of Status when the request could not complete
	// (DNS, connect, TLS, or timeout failure). Any HTTP response at all,
	// including a 4xx/5xx, is not an Error.
	Error     string `json:"error,omitempty"`
	LatencyMs int64  `json:"latency_ms"`
}

// Probe sends a GET request to every service base_url declared in
// e.Services (a service with no base_url is skipped), using client, and
// returns one ProbeResult per probed service, sorted by service name. It
// never returns an error itself: a failed request is recorded as
// ProbeResult.Error rather than aborting the others.
func Probe(ctx context.Context, e domain.Environment, client *http.Client) []ProbeResult {
	names := make([]string, 0, len(e.Services))
	for name, se := range e.Services {
		if se.BaseURL != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	results := make([]ProbeResult, 0, len(names))
	for _, name := range names {
		results = append(results, probeOne(ctx, name, e.Services[name].BaseURL, client))
	}
	return results
}

// probeOne sends one GET request to baseURL and reports the outcome.
func probeOne(ctx context.Context, service, baseURL string, client *http.Client) ProbeResult {
	result := ProbeResult{Service: service, BaseURL: baseURL}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	start := time.Now()
	resp, err := client.Do(req)
	result.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()
	result.Status = resp.StatusCode
	return result
}

// NewProbeClient builds an *http.Client suitable for Probe: redirects are
// followed (net/http's own default cap), no auth is applied, and TLS
// verification is skipped only when e.Transport.InsecureTLS is set and e is
// not a production environment (PLAN §20, §28: insecure_tls is never
// honored in production, regardless of what the file says). timeout, when
// positive, applies as the client's overall per-request deadline; zero
// means no timeout.
func NewProbeClient(e domain.Environment, timeout time.Duration) *http.Client {
	tlsConfig := &tls.Config{}
	if e.Transport != nil && e.Transport.InsecureTLS && !e.Production {
		tlsConfig.InsecureSkipVerify = true
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
	}
}
