package runtime

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

func TestClientDoJSONRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Extra", "one")
		w.Header().Add("X-Extra", "two")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"count": 2, "price": 9.5}`))
	}))
	defer srv.Close()

	c := New(Options{})
	resp, err := c.Do(context.Background(), Request{Method: http.MethodGet, URL: srv.URL})
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.Status)
	body := resp.Body.(map[string]any)
	assert.Equal(t, int64(2), body["count"])
	assert.Equal(t, 9.5, body["price"])
	assert.False(t, resp.Truncated)
	assert.Equal(t, "one, two", resp.Headers["X-Extra"])
	assert.Greater(t, resp.Timings.TotalMs, 0.0)
	assert.Greater(t, resp.Timings.TTFBMs, 0.0)
}

func TestClientDoNonJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer srv.Close()

	c := New(Options{})
	resp, err := c.Do(context.Background(), Request{Method: http.MethodGet, URL: srv.URL})
	require.NoError(t, err)
	assert.Nil(t, resp.Body)
	assert.Equal(t, "not json at all", string(resp.BodyRaw))
	assert.False(t, resp.Truncated)
}

func TestClientDoTruncation(t *testing.T) {
	payload := strings.Repeat("a", 100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	transport := domain.Transport{MaxBodyBytes: 10}
	c := New(Options{Transport: transport})
	resp, err := c.Do(context.Background(), Request{Method: http.MethodGet, URL: srv.URL})
	require.NoError(t, err)

	assert.True(t, resp.Truncated)
	assert.Nil(t, resp.Body)
	assert.Len(t, resp.BodyRaw, 10)
	assert.Greater(t, resp.Size, int64(10))
}

func TestClientDoHeadersAndRequestBody(t *testing.T) {
	var gotContentType, gotUA, gotMethod string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotUA = r.Header.Get("User-Agent")
		gotMethod = r.Method
		gotBody, _ = readAll(r)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := New(Options{UserAgent: "test-agent/1.0"})
	resp, err := c.Do(context.Background(), Request{
		Method: http.MethodPost,
		URL:    srv.URL,
		Body:   map[string]any{"name": "ada"},
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.Status)
	assert.Equal(t, "POST", gotMethod)
	assert.Equal(t, "application/json", gotContentType)
	assert.Equal(t, "test-agent/1.0", gotUA)
	assert.JSONEq(t, `{"name":"ada"}`, string(gotBody))

	// The request-as-sent record reflects the defaults that were applied.
	assert.Equal(t, "application/json", resp.Request.ContentType)
	assert.Equal(t, "application/json", resp.Request.Headers["Content-Type"])
	assert.Equal(t, "test-agent/1.0", resp.Request.Headers["User-Agent"])
}

func TestClientDoDefaultUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Options{})
	_, err := c.Do(context.Background(), Request{Method: http.MethodGet, URL: srv.URL})
	require.NoError(t, err)
	assert.Equal(t, "sapien/dev", gotUA)
}

func TestClientDoRequestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Options{})
	_, err := c.Do(context.Background(), Request{Method: http.MethodGet, URL: srv.URL, Timeout: 20 * time.Millisecond})
	require.Error(t, err)
	e := errs.As(err)
	assert.Equal(t, errs.HTTPTransport, e.Code)
	assert.Contains(t, e.Hint, "timed out after")
}

func TestClientDoExternalCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Options{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := c.Do(ctx, Request{Method: http.MethodGet, URL: srv.URL})
	require.Error(t, err)
	assert.True(t, errs.Is(err, errs.Cancelled), "expected Cancelled, got %v", err)
}

func TestClientDoConnectionRefused(t *testing.T) {
	// Bind a listener, grab its address, then close it: guarantees nothing
	// is listening on that port, so the connection is refused immediately.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	c := New(Options{})
	_, err = c.Do(context.Background(), Request{Method: http.MethodGet, URL: "http://" + addr})
	require.Error(t, err)
	e := errs.As(err)
	assert.Equal(t, errs.HTTPTransport, e.Code)
	assert.Contains(t, e.Hint, "is the service running at")
	assert.Contains(t, e.Hint, addr)
}

func TestClientDoRedirectsAndAuthStripping(t *testing.T) {
	var targetSawAuth string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetSawAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	var originSawAuth string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originSawAuth = r.Header.Get("Authorization")
		http.Redirect(w, r, target.URL+"/dest", http.StatusFound)
	}))
	defer origin.Close()

	c := New(Options{})
	resp, err := c.Do(context.Background(), Request{
		Method:  http.MethodGet,
		URL:     origin.URL,
		Headers: map[string]string{"Authorization": "Bearer secret-token"},
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.Status)
	assert.Equal(t, "Bearer secret-token", originSawAuth)
	assert.Empty(t, targetSawAuth, "Authorization must not be forwarded across hosts on redirect")
}

func TestClientDoMaxRedirects(t *testing.T) {
	var mux http.HandlerFunc
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mux(w, r) }))
	defer srv.Close()
	mux = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+r.URL.Path+"x", http.StatusFound)
	}

	c := New(Options{Transport: domain.Transport{MaxRedirects: 2}})
	_, err := c.Do(context.Background(), Request{Method: http.MethodGet, URL: srv.URL + "/a"})
	require.Error(t, err)
}

func TestClientDoInsecureTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Run("insecure allowed in non-production", func(t *testing.T) {
		c := New(Options{Transport: domain.Transport{InsecureTLS: true}, Production: false})
		resp, err := c.Do(context.Background(), Request{Method: http.MethodGet, URL: srv.URL})
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.Status)
	})

	t.Run("insecure ignored in production", func(t *testing.T) {
		c := New(Options{Transport: domain.Transport{InsecureTLS: true}, Production: true})
		_, err := c.Do(context.Background(), Request{Method: http.MethodGet, URL: srv.URL})
		require.Error(t, err)
		e := errs.As(err)
		assert.Equal(t, errs.HTTPTransport, e.Code)
		assert.Contains(t, e.Hint, "insecure_tls")
	})

	t.Run("verification fails without insecure flag", func(t *testing.T) {
		c := New(Options{})
		_, err := c.Do(context.Background(), Request{Method: http.MethodGet, URL: srv.URL})
		require.Error(t, err)
	})
}

func readAll(r *http.Request) ([]byte, error) {
	buf := make([]byte, 0)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf, nil
}

func TestClientDoRawStringBody(t *testing.T) {
	var gotBody []byte
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = readAll(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Options{})
	_, err := c.Do(context.Background(), Request{
		Method:      http.MethodPost,
		URL:         srv.URL,
		Body:        "plain text body",
		ContentType: "text/plain",
	})
	require.NoError(t, err)
	assert.Equal(t, "plain text body", string(gotBody))
	assert.Equal(t, "text/plain", gotCT)
}

func TestClientDoEncodeBodyError(t *testing.T) {
	c := New(Options{})
	_, err := c.Do(context.Background(), Request{
		Method: http.MethodPost,
		URL:    "http://example.com",
		Body:   func() {}, // not JSON-marshalable
	})
	require.Error(t, err)
	assert.True(t, errs.Is(err, errs.Invalid))
}

func TestClientDoMethodDefault(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Options{})
	_, err := c.Do(context.Background(), Request{URL: srv.URL})
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, gotMethod)
}

func TestNewProxyOptions(t *testing.T) {
	t.Run("empty means no proxy func", func(t *testing.T) {
		c := New(Options{})
		tr := c.http.Transport.(*http.Transport)
		assert.Nil(t, tr.Proxy)
	})

	t.Run("env uses ProxyFromEnvironment", func(t *testing.T) {
		c := New(Options{Transport: domain.Transport{Proxy: "env"}})
		tr := c.http.Transport.(*http.Transport)
		require.NotNil(t, tr.Proxy)
	})

	t.Run("explicit proxy URL used", func(t *testing.T) {
		c := New(Options{Transport: domain.Transport{Proxy: "http://proxy.local:8080"}})
		tr := c.http.Transport.(*http.Transport)
		require.NotNil(t, tr.Proxy)
		req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
		u, err := tr.Proxy(req)
		require.NoError(t, err)
		assert.Equal(t, "proxy.local:8080", u.Host)
	})
}

func TestClassifyErrorHelpers(t *testing.T) {
	assert.False(t, isConnRefused(fmt.Errorf("some other error")))
	assert.False(t, isTLSError(fmt.Errorf("some other error")))
}
