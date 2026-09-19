package semantic_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/semantic"
)

// limitedOllama mimics an Ollama /api/embed whose model refuses any input
// longer than limit characters the way Ollama does -- a 400 for the whole
// request, truncate or not -- and records what it was sent.
type limitedOllama struct {
	*httptest.Server
	limit int

	mu        sync.Mutex
	requests  int
	accepted  []string
	keepAlive []string
}

func newLimitedOllama(t *testing.T, limit int) *limitedOllama {
	t.Helper()
	l := &limitedOllama{limit: limit}
	l.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input     []string `json:"input"`
			KeepAlive string   `json:"keep_alive"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		l.mu.Lock()
		l.requests++
		l.keepAlive = append(l.keepAlive, req.KeepAlive)
		l.mu.Unlock()
		for _, in := range req.Input {
			if len([]rune(in)) > l.limit {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"the input length exceeds the context length"}`))
				return
			}
		}
		l.mu.Lock()
		l.accepted = append(l.accepted, req.Input...)
		l.mu.Unlock()
		out := make([][]float32, len(req.Input))
		for i, in := range req.Input {
			out[i] = hashEmbed(in)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
	t.Cleanup(l.Close)
	return l
}

// TestEmbed_InputOverTheModelsContextIsCutToFit: one over-long input used to
// fail its whole batch -- and with it the whole indexing pass -- with
// Ollama's "the input length exceeds the context length". It is now embedded
// from its head, the others in its batch are embedded whole, and the length
// that had to be cut to is remembered so the next long input is clipped
// before it is sent rather than refused again.
func TestEmbed_InputOverTheModelsContextIsCutToFit(t *testing.T) {
	srv := newLimitedOllama(t, 1000)
	emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{Kind: "ollama", BaseURL: srv.URL, Model: "m", BatchSize: 8})
	require.NoError(t, err)

	long := "HEAD " + strings.Repeat("x", 5000)
	vecs, err := emb.Embed(context.Background(), []string{"short one", long, "short two"})
	require.NoError(t, err)
	require.Len(t, vecs, 3, "one vector per input, in order")

	srv.mu.Lock()
	var cut string
	for _, a := range srv.accepted {
		if strings.HasPrefix(a, "HEAD ") {
			cut = a
		}
	}
	assert.Contains(t, srv.accepted, "short one")
	assert.Contains(t, srv.accepted, "short two")
	before := srv.requests
	srv.mu.Unlock()
	require.NotEmpty(t, cut, "the long input was embedded from its head")
	assert.LessOrEqual(t, len([]rune(cut)), 1000)

	// The limit is learned: a second long input goes through first time.
	_, err = emb.Embed(context.Background(), []string{"HEAD " + strings.Repeat("y", 9000)})
	require.NoError(t, err)
	srv.mu.Lock()
	defer srv.mu.Unlock()
	assert.Equal(t, before+1, srv.requests, "clipped up front: one request, no refusal")
}

func TestEmbed_AnInputThatNeverFitsStillReportsTheProvidersRefusal(t *testing.T) {
	srv := newLimitedOllama(t, 10) // below the floor embedShrinking cuts to
	emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{Kind: "ollama", BaseURL: srv.URL, Model: "m"})
	require.NoError(t, err)
	_, err = emb.Embed(context.Background(), []string{strings.Repeat("z", 4000)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context length")
}

func TestEmbed_OllamaKeepAliveIsSentOnlyWhenConfigured(t *testing.T) {
	srv := newLimitedOllama(t, 1000)
	ctx := context.Background()

	plain, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{Kind: "ollama", BaseURL: srv.URL, Model: "m"})
	require.NoError(t, err)
	_, err = plain.Embed(ctx, []string{"a"})
	require.NoError(t, err)

	brief, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{Kind: "ollama", BaseURL: srv.URL, Model: "m", KeepAlive: "30s"})
	require.NoError(t, err)
	_, err = brief.Embed(ctx, []string{"b"})
	require.NoError(t, err)

	srv.mu.Lock()
	defer srv.mu.Unlock()
	assert.Equal(t, []string{"", "30s"}, srv.keepAlive)
}
