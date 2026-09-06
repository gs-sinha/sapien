package env

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
)

// unreachableURL returns an http:// URL nothing is listening on: a fresh
// loopback listener is opened and immediately closed, so connecting to it
// fails fast (connection refused) instead of timing out.
func unreachableURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return "http://" + addr
}

func TestProbe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := domain.Environment{
		Name: "local",
		Services: map[string]domain.ServiceEnv{
			"order-service": {BaseURL: srv.URL},
		},
	}
	client := NewProbeClient(e, 5*time.Second)
	results := Probe(context.Background(), e, client)

	require.Len(t, results, 1)
	assert.Equal(t, "order-service", results[0].Service)
	assert.Equal(t, srv.URL, results[0].BaseURL)
	assert.Equal(t, http.StatusOK, results[0].Status)
	assert.Empty(t, results[0].Error)
	assert.GreaterOrEqual(t, results[0].LatencyMs, int64(0))
}

func TestProbe_NonOKStatusIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	e := domain.Environment{
		Services: map[string]domain.ServiceEnv{"svc": {BaseURL: srv.URL}},
	}
	results := Probe(context.Background(), e, NewProbeClient(e, 5*time.Second))

	require.Len(t, results, 1)
	assert.Equal(t, http.StatusInternalServerError, results[0].Status)
	assert.Empty(t, results[0].Error, "any HTTP response, even a 5xx, is not an Error")
}

func TestProbe_Unreachable(t *testing.T) {
	e := domain.Environment{
		Services: map[string]domain.ServiceEnv{"svc": {BaseURL: unreachableURL(t)}},
	}
	results := Probe(context.Background(), e, NewProbeClient(e, 5*time.Second))

	require.Len(t, results, 1)
	assert.Equal(t, 0, results[0].Status)
	assert.NotEmpty(t, results[0].Error)
}

func TestProbe_Timeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	// Close (which waits for the blocked handler to return) must run before
	// closing block (which is what lets the handler return), so declare
	// this defer second: defers run last-declared-first.
	defer srv.Close()
	defer close(block)

	e := domain.Environment{
		Services: map[string]domain.ServiceEnv{"svc": {BaseURL: srv.URL}},
	}
	client := NewProbeClient(e, 50*time.Millisecond)
	results := Probe(context.Background(), e, client)

	require.Len(t, results, 1)
	assert.NotEmpty(t, results[0].Error)
	assert.Equal(t, 0, results[0].Status)
}

func TestProbe_SkipsServicesWithNoBaseURL(t *testing.T) {
	e := domain.Environment{
		Services: map[string]domain.ServiceEnv{
			"no-url": {},
		},
	}
	results := Probe(context.Background(), e, NewProbeClient(e, time.Second))
	assert.Empty(t, results)
}

func TestProbe_SortedByServiceName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := domain.Environment{
		Services: map[string]domain.ServiceEnv{
			"zzz": {BaseURL: srv.URL},
			"aaa": {BaseURL: srv.URL},
		},
	}
	results := Probe(context.Background(), e, NewProbeClient(e, time.Second))
	require.Len(t, results, 2)
	assert.Equal(t, "aaa", results[0].Service)
	assert.Equal(t, "zzz", results[1].Service)
}

func TestNewProbeClient_InsecureTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Without insecure_tls, the self-signed cert is rejected.
	plain := domain.Environment{Services: map[string]domain.ServiceEnv{"svc": {BaseURL: srv.URL}}}
	results := Probe(context.Background(), plain, NewProbeClient(plain, 5*time.Second))
	require.Len(t, results, 1)
	assert.NotEmpty(t, results[0].Error)

	// With insecure_tls set on a non-production environment, it succeeds.
	insecure := domain.Environment{
		Services:  map[string]domain.ServiceEnv{"svc": {BaseURL: srv.URL}},
		Transport: &domain.Transport{InsecureTLS: true},
	}
	results = Probe(context.Background(), insecure, NewProbeClient(insecure, 5*time.Second))
	require.Len(t, results, 1)
	assert.Empty(t, results[0].Error)
	assert.Equal(t, http.StatusOK, results[0].Status)
}

func TestNewProbeClient_InsecureTLSNeverHonoredInProduction(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	prod := domain.Environment{
		Production: true,
		Services:   map[string]domain.ServiceEnv{"svc": {BaseURL: srv.URL}},
		Transport:  &domain.Transport{InsecureTLS: true},
	}
	results := Probe(context.Background(), prod, NewProbeClient(prod, 5*time.Second))
	require.Len(t, results, 1)
	assert.NotEmpty(t, results[0].Error, "insecure_tls must never be honored in production")
}
