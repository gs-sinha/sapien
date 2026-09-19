package semantic_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/semantic"
)

// hashEmbed deterministically embeds text into 16 dims by hashing its
// lower-cased whitespace-split words into buckets, so texts sharing words
// end up with overlapping non-zero dimensions ("semantically closer" for
// the purposes of a fake test server).
func hashEmbed(text string) []float32 {
	const dims = 16
	v := make([]float32, dims)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		h := uint32(2166136261)
		for _, c := range []byte(w) {
			h ^= uint32(c)
			h *= 16777619
		}
		v[h%dims] += 1
	}
	return v
}

// newFakeOpenAIServer returns an httptest.Server that mimics an
// OpenAI-compatible /embeddings endpoint: it asserts the request shape
// ({model, input[]}, an Authorization header when wantAuth is set) and
// responds with deterministic hash-based embeddings, one per input, in
// input order.
func newFakeOpenAIServer(t *testing.T, wantModel, wantAuth string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/embeddings", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		if wantAuth != "" {
			assert.Equal(t, "Bearer "+wantAuth, r.Header.Get("Authorization"))
		}

		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, wantModel, req.Model)
		require.NotEmpty(t, req.Input)

		type item struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		}
		resp := struct {
			Data []item `json:"data"`
		}{}
		for i, in := range req.Input {
			resp.Data = append(resp.Data, item{Embedding: hashEmbed(in), Index: i})
		}

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
}

// newFakeOllamaServer mimics Ollama's POST /api/embed.
func newFakeOllamaServer(t *testing.T, wantModel string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/embed", r.URL.Path)

		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, wantModel, req.Model)
		require.NotEmpty(t, req.Input)

		embeddings := make([][]float32, len(req.Input))
		for i, in := range req.Input {
			embeddings[i] = hashEmbed(in)
		}

		resp := struct {
			Embeddings [][]float32 `json:"embeddings"`
		}{Embeddings: embeddings}

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
}

func TestNewHTTPEmbedder_OpenAI(t *testing.T) {
	srv := newFakeOpenAIServer(t, "text-embedding-3-small", "sk-literal-key")
	defer srv.Close()

	emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{
		Kind:    "openai",
		BaseURL: srv.URL,
		APIKey:  "sk-literal-key",
		Model:   "text-embedding-3-small",
	})
	require.NoError(t, err)
	assert.Equal(t, "text-embedding-3-small", emb.Model())
	assert.Equal(t, 0, emb.Dim(), "Dim is unknown before the first Embed call")

	vecs, err := emb.Embed(context.Background(), []string{"hello world", "goodbye world"})
	require.NoError(t, err)
	require.Len(t, vecs, 2)
	assert.Len(t, vecs[0], 16)
	assert.Equal(t, 16, emb.Dim(), "Dim is learned from the first response")

	// Texts sharing the word "world" should share a non-zero dimension.
	shared := false
	for i := range vecs[0] {
		if vecs[0][i] > 0 && vecs[1][i] > 0 {
			shared = true
		}
	}
	assert.True(t, shared, "texts sharing a word should share a dimension")
}

func TestNewHTTPEmbedder_Ollama(t *testing.T) {
	srv := newFakeOllamaServer(t, "nomic-embed-text")
	defer srv.Close()

	emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{
		Kind:    "ollama",
		BaseURL: srv.URL,
		Model:   "nomic-embed-text",
	})
	require.NoError(t, err)

	vecs, err := emb.Embed(context.Background(), []string{"find couriers near a pickup"})
	require.NoError(t, err)
	require.Len(t, vecs, 1)
	assert.Len(t, vecs[0], 16)
}

func TestNewHTTPEmbedder_APIKeyFromEnvBraceForm(t *testing.T) {
	t.Setenv("SAPIEN_TEST_API_KEY", "env-resolved-key")
	srv := newFakeOpenAIServer(t, "m", "env-resolved-key")
	defer srv.Close()

	emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{
		Kind:    "openai",
		BaseURL: srv.URL,
		APIKey:  "${env.SAPIEN_TEST_API_KEY}",
		Model:   "m",
	})
	require.NoError(t, err)

	_, err = emb.Embed(context.Background(), []string{"x"})
	require.NoError(t, err)
}

func TestNewHTTPEmbedder_APIKeyFromEnvDollarForm(t *testing.T) {
	t.Setenv("SAPIEN_TEST_API_KEY_2", "env-resolved-key-2")
	srv := newFakeOpenAIServer(t, "m", "env-resolved-key-2")
	defer srv.Close()

	emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{
		Kind:    "openai",
		BaseURL: srv.URL,
		APIKey:  "$SAPIEN_TEST_API_KEY_2",
		Model:   "m",
	})
	require.NoError(t, err)

	_, err = emb.Embed(context.Background(), []string{"x"})
	require.NoError(t, err)
}

func TestNewHTTPEmbedder_APIKeyLiteralWhenNotAnEnvReference(t *testing.T) {
	// A literal that happens not to start with "$" is used as-is.
	os.Unsetenv("SAPIEN_TEST_API_KEY_UNSET") // sanity: make sure it really is unset
	srv := newFakeOpenAIServer(t, "m", "literal-value")
	defer srv.Close()

	emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{
		Kind:    "openai",
		BaseURL: srv.URL,
		APIKey:  "literal-value",
		Model:   "m",
	})
	require.NoError(t, err)
	_, err = emb.Embed(context.Background(), []string{"x"})
	require.NoError(t, err)
}

func TestNewHTTPEmbedder_NoAPIKeySendsNoAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[1,2,3]]}`))
	}))
	defer srv.Close()

	emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{
		Kind:    "ollama",
		BaseURL: srv.URL,
		Model:   "m",
	})
	require.NoError(t, err)
	_, err = emb.Embed(context.Background(), []string{"x"})
	require.NoError(t, err)
}

func TestNewHTTPEmbedder_BatchSizeSplitsRequests(t *testing.T) {
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		var req struct {
			Input []string `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.LessOrEqual(t, len(req.Input), 2)

		type item struct {
			Embedding []float32 `json:"embedding"`
		}
		resp := struct {
			Data []item `json:"data"`
		}{}
		for _, in := range req.Input {
			resp.Data = append(resp.Data, item{Embedding: hashEmbed(in)})
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{
		Kind:      "openai",
		BaseURL:   srv.URL,
		Model:     "m",
		BatchSize: 2,
	})
	require.NoError(t, err)

	vecs, err := emb.Embed(context.Background(), []string{"a", "b", "c", "d", "e"})
	require.NoError(t, err)
	assert.Len(t, vecs, 5)
	assert.Equal(t, 3, requestCount, "5 items at batch size 2 should take 3 requests")
}

func TestNewHTTPEmbedder_DefaultBatchSizeAndTimeout(t *testing.T) {
	srv := newFakeOpenAIServer(t, "m", "")
	defer srv.Close()

	emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{
		Kind:    "openai",
		BaseURL: srv.URL,
		Model:   "m",
		// BatchSize and Timeout left zero: defaults (32, 120s) apply.
	})
	require.NoError(t, err)
	_, err = emb.Embed(context.Background(), []string{"a"})
	require.NoError(t, err)
}

func TestNewHTTPEmbedder_InvalidKind(t *testing.T) {
	_, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{Kind: "bogus", BaseURL: "http://x", Model: "m"})
	assert.Error(t, err)
}

func TestNewHTTPEmbedder_MissingBaseURL(t *testing.T) {
	_, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{Kind: "openai", Model: "m"})
	assert.Error(t, err)
}

func TestNewHTTPEmbedder_MissingModel(t *testing.T) {
	_, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{Kind: "openai", BaseURL: "http://x"})
	assert.Error(t, err)
}

func TestNewHTTPEmbedder_EmptyInputNoRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{Kind: "openai", BaseURL: srv.URL, Model: "m"})
	require.NoError(t, err)

	vecs, err := emb.Embed(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, vecs)
	assert.False(t, called)
}

func TestNewHTTPEmbedder_ServerErrorSurfacesStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{Kind: "openai", BaseURL: srv.URL, Model: "m"})
	require.NoError(t, err)

	_, err = emb.Embed(context.Background(), []string{"x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), "boom")
}

func TestNewHTTPEmbedder_MismatchedResponseCountErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,2,3]}]}`)) // 1 embedding for 2 inputs
	}))
	defer srv.Close()

	emb, err := semantic.NewHTTPEmbedder(semantic.HTTPConfig{Kind: "openai", BaseURL: srv.URL, Model: "m"})
	require.NoError(t, err)

	_, err = emb.Embed(context.Background(), []string{"x", "y"})
	require.Error(t, err)
}
