package local

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/semantic"
	"github.com/growsimplee/sapien/internal/store"
)

// openLocalTestDB opens a fresh, migrated in-memory store for one test --
// used by the semanticAdapter unit tests below, which exercise it directly
// rather than through a full Open (no workspace/catalog needed).
func openLocalTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// fakeEmbedder is a deterministic, in-process stand-in for
// semantic.Embedder: it hash-embeds text into a fixed number of dimensions
// by bucketing each lower-cased, whitespace-split word (mirroring the
// pattern internal/semantic's own tests use for a fake OpenAI-compatible
// server, just without an actual HTTP round trip -- local.Options.Embedder
// takes a semantic.Embedder value directly, so there is nothing to stand up
// a server for). Two texts sharing a word land a nonzero dot product in
// that word's bucket; texts sharing no words are (bar hash collisions)
// unrelated.
type fakeEmbedder struct {
	model string
	dim   int

	mu    sync.Mutex
	calls int
}

func newFakeEmbedder(model string) *fakeEmbedder {
	return &fakeEmbedder{model: model, dim: 32}
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()

	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = hashEmbed(t, f.dim)
	}
	return out, nil
}

func (f *fakeEmbedder) Model() string { return f.model }
func (f *fakeEmbedder) Dim() int      { return f.dim }

func (f *fakeEmbedder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// hashEmbed deterministically embeds text into dims dimensions by hashing
// its lower-cased whitespace-split words into buckets (FNV-1a-style),
// exactly the technique internal/semantic's own tests use.
func hashEmbed(text string, dims int) []float32 {
	v := make([]float32, dims)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		h := uint32(2166136261)
		for _, c := range []byte(w) {
			h ^= uint32(c)
			h *= 16777619
		}
		v[h%uint32(dims)]++
	}
	return v
}

// erroringEmbedder always fails, simulating an unreachable embedding
// endpoint (PLAN §16 / task spec: search must still work lexically).
type erroringEmbedder struct{}

func (erroringEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("embedding endpoint unreachable")
}
func (erroringEmbedder) Model() string { return "erroring" }
func (erroringEmbedder) Dim() int      { return 0 }

// addFixtureServices copies the three logistics fixtures into a fresh temp
// dir and registers each one via Services().Add (task spec: "after
// Services().Add of the three fixtures"), returning the added services.
func addFixtureServices(t *testing.T, l *Local) []domain.Service {
	t.Helper()
	ctx := context.Background()
	fixDir := copyFixtures(t)

	var out []domain.Service
	for _, name := range []string{"order-service", "allocation-service", "rider-service"} {
		svc, err := l.Services().Add(ctx, name,
			domain.Source{Kind: domain.SourceLocal, Path: filepath.Join(fixDir, name)})
		require.NoError(t, err)
		require.NotNil(t, svc)
		require.Equal(t, domain.SyncOK, svc.Status)
		out = append(out, *svc)
	}
	return out
}

// waitForSemanticStats polls SemanticStats until check reports true, or
// fails the test after 3 seconds (PLAN §16 / task spec: indexing happens on
// a background goroutine, so tests poll rather than assert immediately).
func waitForSemanticStats(t *testing.T, l *Local, check func(semantic.Stats) bool) semantic.Stats {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last semantic.Stats
	for {
		stats, err := l.SemanticStats(context.Background())
		require.NoError(t, err)
		last = stats
		if check(stats) {
			return stats
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for semantic stats condition; last stats: %+v", last)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSemantic_DisabledByDefault(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	stats, err := l.SemanticStats(context.Background())
	require.NoError(t, err)
	assert.Equal(t, semantic.Stats{}, stats)

	require.NoError(t, l.SemanticReindex(context.Background()), "SemanticReindex must be a no-op when disabled")
}

func TestSemantic_IndexesOperationsAndDocsAfterAdd(t *testing.T) {
	ws, _ := setupWorkspace(t)
	// setupWorkspace already registers the three fixtures as *local* sources
	// and Open's staleCheck would sync them; drop them so this test controls
	// the Add sequence itself, per the task spec ("after Services().Add of
	// the three fixtures").
	ws.Services = nil

	emb := newFakeEmbedder("fake-v1")
	l, err := Open(ws, Options{Embedder: emb})
	require.NoError(t, err)
	defer l.Close()

	addFixtureServices(t, l)

	stats := waitForSemanticStats(t, l, func(s semantic.Stats) bool {
		return s.ByKind[semantic.KindOperation] >= 15 && s.ByKind[semantic.KindDoc] > 0
	})
	assert.GreaterOrEqual(t, stats.ByKind[semantic.KindOperation], 15)
	assert.Positive(t, stats.ByKind[semantic.KindDoc])
	assert.Positive(t, stats.Total)
	require.Len(t, stats.Models, 1)
	assert.Equal(t, "fake-v1", stats.Models[0].Model)
}

func TestSemantic_Reindex_IsSynchronousAndCoversMemories(t *testing.T) {
	ws, _ := setupWorkspace(t)
	ws.Services = nil

	emb := newFakeEmbedder("fake-v1")
	l, err := Open(ws, Options{Embedder: emb})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	addFixtureServices(t, l)
	_, err = l.Memories().Create(ctx, domain.Memory{
		Scope: domain.ScopeWorkspace,
		Text:  "Riders in Mumbai sometimes report GPS drift near the harbor.",
	})
	require.NoError(t, err)

	// SemanticReindex is synchronous: no polling needed, unlike the
	// background per-sync/per-write hook exercised by the other tests here.
	require.NoError(t, l.SemanticReindex(ctx))

	stats, err := l.SemanticStats(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats.ByKind[semantic.KindOperation], 15)
	assert.Positive(t, stats.ByKind[semantic.KindDoc])
	assert.Equal(t, 1, stats.ByKind[semantic.KindMemory])
}

// TestSemantic_QueryBridgesLexicalMiss exercises PLAN §16's hybrid search:
// a query with zero lexical overlap anywhere in rider-service.searchRiders'
// indexed text (op_id, path, summary, description, tags, param/field names)
// still surfaces it, tagged "semantic", once the fake embedder's bag-of-words
// vectors relate the two texts. With only 15 real operations indexed and
// Query's default fan-out (limit*2 = 20, PLAN §16), every operation is a
// semantic "hit" of some score; what this test actually verifies is the
// integration -- that search.Searcher.WithSemantic really is wired to a live
// *semantic.Index backed by the fake embedder, that the "misses lexically"
// half of the property genuinely holds against the real fixture text, and
// that the fused result carries the "semantic" MatchedOn tag Operations()
// only ever adds for a semantic-sourced hit.
//
// The "misses lexically" half is checked against a wholly separate,
// semantic-disabled *Local over the same fixture content, rather than by
// calling the semantic-enabled instance's own searcher with a hand-rolled
// "lexical-only" option: search.Searcher.WithSemantic mutates the searcher
// in place, so there is no way to get a pre-fusion lexical result back out
// of an instance that already has semantic wired.
func TestSemantic_QueryBridgesLexicalMiss(t *testing.T) {
	const query = "couriers close to a customer"

	lexicalOnlyWS, _ := setupWorkspace(t)
	lexicalOnly, err := Open(lexicalOnlyWS, Options{})
	require.NoError(t, err)
	defer lexicalOnly.Close()

	lexical, err := lexicalOnly.Search().Operations(context.Background(), query, domain.SearchOptions{})
	require.NoError(t, err)
	for _, r := range lexical {
		assert.NotEqual(t, "rider-service.searchRiders", r.Operation.ID,
			"lexical search alone should not find searchRiders for this query")
	}

	ws, _ := setupWorkspace(t)
	ws.Services = nil

	emb := newFakeEmbedder("fake-v1")
	l, err := Open(ws, Options{Embedder: emb})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	addFixtureServices(t, l)
	waitForSemanticStats(t, l, func(s semantic.Stats) bool {
		return s.ByKind[semantic.KindOperation] >= 15
	})

	results, err := l.Search().Operations(ctx, query, domain.SearchOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, results)

	var found *domain.SearchResult
	for i := range results {
		if results[i].Operation.ID == "rider-service.searchRiders" {
			found = &results[i]
		}
	}
	require.NotNil(t, found, "the semantic-fused results must include searchRiders")
	assert.Contains(t, found.MatchedOn, "semantic")
}

func TestSemantic_EmbeddingFailure_FallsBackToLexical(t *testing.T) {
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{Embedder: erroringEmbedder{}})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	// The background indexing hook fires (Open's staleCheck synced the
	// three fixtures already registered by setupWorkspace) but every embed
	// call fails; that must never surface anywhere -- give it a moment,
	// then confirm search still answers normally.
	time.Sleep(100 * time.Millisecond)

	results, err := l.Search().Operations(ctx, "allocate rider", domain.SearchOptions{})
	require.NoError(t, err, "a failing embedder must never make lexical search return an error")
	require.NotEmpty(t, results)
	assert.Equal(t, "allocation-service.allocate", results[0].Operation.ID)
	for _, r := range results {
		assert.NotContains(t, r.MatchedOn, "semantic")
	}

	stats, err := l.SemanticStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Total, "every embed call failed, so nothing should have been written")
}

func TestSemanticAdapter_QueryErrorIsSwallowed(t *testing.T) {
	db := openLocalTestDB(t)
	idx := semantic.NewIndex(db, erroringEmbedder{})
	a := &semanticAdapter{idx: idx}

	hits, err := a.Query(context.Background(), "operation", "anything", 5)
	require.NoError(t, err)
	assert.Nil(t, hits)
}

func TestSemanticAdapter_QuerySuccessMapsHits(t *testing.T) {
	db := openLocalTestDB(t)
	emb := newFakeEmbedder("m")
	idx := semantic.NewIndex(db, emb)
	ctx := context.Background()

	_, err := idx.IndexOperations(ctx, []domain.Operation{{
		ID:      "svc.op",
		Summary: "Find riders near a pickup",
	}}, nil)
	require.NoError(t, err)

	a := &semanticAdapter{idx: idx}
	hits, err := a.Query(ctx, "operation", "find riders near a pickup", 5)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "svc.op", hits[0].ID)
}

// fakeOpenAIEmbeddingServer mimics an OpenAI-compatible /embeddings
// endpoint with the same hash-based embedding fakeEmbedder uses, so a
// config-driven (as opposed to Options.Embedder-driven) Open exercises the
// real semantic.NewHTTPEmbedder path end to end.
func fakeOpenAIEmbeddingServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

		type item struct {
			Embedding []float32 `json:"embedding"`
		}
		resp := struct {
			Data []item `json:"data"`
		}{}
		for _, in := range req.Input {
			resp.Data = append(resp.Data, item{Embedding: hashEmbed(in, 32)})
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
}

// writeWorkspaceConfig writes ws's workspace-level config.yaml
// (<ws.Dir>/.sapien/config.yaml, per internal/config.WorkspacePath).
func writeWorkspaceConfig(t *testing.T, ws *domain.Workspace, contents string) {
	t.Helper()
	dir := filepath.Join(ws.Dir, domain.WorkspaceStateDir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(contents), 0o644))
}

func TestSemantic_EnabledViaConfig_UsesRealHTTPEmbedder(t *testing.T) {
	srv := fakeOpenAIEmbeddingServer(t)
	defer srv.Close()

	ws, _ := setupWorkspace(t)
	writeWorkspaceConfig(t, ws, `
semantic:
  enabled: true
  kind: openai
  base_url: `+srv.URL+`
  model: test-model
`)

	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()

	require.NotNil(t, l.semIdx, "config.Semantic.Enabled must build a real embedder when Options.Embedder is unset")

	waitForSemanticStats(t, l, func(s semantic.Stats) bool {
		return s.ByKind[semantic.KindOperation] >= 15
	})
}

func TestSemantic_EnabledViaConfig_InvalidKindFailsOpen(t *testing.T) {
	ws, _ := setupWorkspace(t)
	writeWorkspaceConfig(t, ws, `
semantic:
  enabled: true
  kind: not-a-real-kind
  base_url: http://127.0.0.1:1
  model: m
`)

	_, err := Open(ws, Options{})
	require.Error(t, err)
}

func TestSemantic_OptionsEmbedderBypassesConfig(t *testing.T) {
	ws, _ := setupWorkspace(t)
	// A config that would fail NewHTTPEmbedder validation if it were ever
	// consulted -- Options.Embedder must bypass it entirely.
	writeWorkspaceConfig(t, ws, `
semantic:
  enabled: true
  kind: not-a-real-kind
`)

	l, err := Open(ws, Options{Embedder: newFakeEmbedder("bypass")})
	require.NoError(t, err)
	defer l.Close()

	require.NotNil(t, l.semIdx)
}
