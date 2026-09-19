package local

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/semantic"
)

// fakeOllamaEmbeddingServer mimics Ollama's POST /api/embed
// {model, input[]} -> {embeddings: [][]float32]} shape (internal/semantic.
// httpEmbedder.embedOllama), using the same hash-based embedding
// fakeEmbedder uses elsewhere in this package, so a config-driven Open/
// ApplySemantic against it exercises the real HTTP embedder path.
func fakeOllamaEmbeddingServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

		embeddings := make([][]float32, len(req.Input))
		for i, in := range req.Input {
			embeddings[i] = hashEmbed(in, 16)
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(struct {
			Embeddings [][]float32 `json:"embeddings"`
		}{Embeddings: embeddings}))
	}))
}

// TestApplySemantic_ConcurrentSearchDuringSwap races Search().Operations
// against repeated ApplySemantic hot-swaps (on -> a different model -> off
// -> on again) for the duration of the test, proving (under `go test
// -race`, PLAN §34f item 5) that a swap never lets a reader observe a
// half-installed embedder: every search either completes against the old
// wiring or the new one, never a torn mix, and nothing panics or
// deadlocks.
func TestApplySemantic_ConcurrentSearchDuringSwap(t *testing.T) {
	srvA := fakeOllamaEmbeddingServer(t)
	defer srvA.Close()
	srvB := fakeOllamaEmbeddingServer(t)
	defer srvB.Close()

	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	ctx := context.Background()
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Concurrent searchers, running for the whole swap sequence below.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, err := l.Search().Operations(ctx, "allocate rider", domain.SearchOptions{})
				assert.NoError(t, err)
				_, err = l.Search().Docs(ctx, "allocation rules", domain.SearchOptions{})
				assert.NoError(t, err)
			}
		}()
	}

	// A concurrent reader of status/stats, so SemanticStatus/SemanticStats
	// (which also take semMu's read lock) are exercised under the same
	// race.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, err := l.SemanticStatus(ctx)
			assert.NoError(t, err)
		}
	}()

	configs := []config.Semantic{
		{Enabled: true, Kind: "ollama", BaseURL: srvA.URL, Model: "model-a"},
		{Enabled: true, Kind: "ollama", BaseURL: srvB.URL, Model: "model-b"},
		{Enabled: false},
		{Enabled: true, Kind: "ollama", BaseURL: srvA.URL, Model: "model-a"},
	}
	for round := 0; round < 5; round++ {
		for _, cfg := range configs {
			require.NoError(t, l.ApplySemantic(cfg))
			time.Sleep(2 * time.Millisecond)
		}
	}

	close(stop)
	wg.Wait()

	// Leave semantic search on so the deferred l.Close() also exercises
	// draining an active worker.
	require.NoError(t, l.ApplySemantic(config.Semantic{Enabled: true, Kind: "ollama", BaseURL: srvA.URL, Model: "model-a"}))
}

// TestApplySemantic_TurningOffLeavesSearchLexicalOnly proves ApplySemantic
// with Enabled: false clears the searcher's semantic backend (search.
// Searcher.WithSemantic(nil)), not just Local's own bookkeeping: a query
// that only a semantic match would surface (see semantic_test.go's
// TestSemantic_QueryBridgesLexicalMiss) stops finding it once turned off.
func TestApplySemantic_TurningOffLeavesSearchLexicalOnly(t *testing.T) {
	ws, _ := setupWorkspace(t)
	emb := newFakeEmbedder("fake-v1")
	l, err := Open(ws, Options{Embedder: emb})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	waitForSemanticStats(t, l, func(s semantic.Stats) bool {
		return s.ByKind[semantic.KindOperation] >= 15
	})

	const query = "couriers close to a customer"
	before, err := l.Search().Operations(ctx, query, domain.SearchOptions{})
	require.NoError(t, err)
	foundBefore := false
	for _, r := range before {
		if r.Operation.ID == "rider-service.searchRiders" {
			foundBefore = true
		}
	}
	require.True(t, foundBefore, "sanity check: semantic fusion must find it while on")

	require.NoError(t, l.ApplySemantic(config.Semantic{Enabled: false}))

	after, err := l.Search().Operations(ctx, query, domain.SearchOptions{})
	require.NoError(t, err)
	for _, r := range after {
		assert.NotEqual(t, "rider-service.searchRiders", r.Operation.ID, "lexical-only search must not find it once semantic search is off")
	}

	status, err := l.SemanticStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, domain.SemanticOff, status.State)
}
