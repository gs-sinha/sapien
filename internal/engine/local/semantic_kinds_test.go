package local

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
	"github.com/gs-sinha/sapien/internal/semantic"
)

// capturingOllama is fakeOllamaEmbeddingServer that also keeps every text it
// was asked to embed, so a test can see what reached the model.
type capturingOllama struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newCapturingOllama(t *testing.T) *capturingOllama {
	t.Helper()
	c := &capturingOllama{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		c.seen = append(c.seen, req.Input...)
		c.mu.Unlock()
		out := make([][]float32, len(req.Input))
		for i, in := range req.Input {
			out[i] = hashEmbed(in, 16)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *capturingOllama) sawContaining(sub string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.seen) - 1; i >= 0; i-- {
		if strings.Contains(c.seen[i], sub) {
			return c.seen[i], true
		}
	}
	return "", false
}

// TestSemanticKinds_TurningDocsOffDropsTheirVectorsAndExamplesRideTheirOperation
// covers PLAN §34f item 5's "what to embed": a kind left out of `kinds` is
// not indexed and the rows it had are dropped; the examples kind puts an
// example's description into the text of the operation it calls; and the
// document prefix in force reaches the model in front of that text.
func TestSemanticKinds_TurningDocsOffDropsTheirVectorsAndExamplesRideTheirOperation(t *testing.T) {
	srv := newCapturingOllama(t)
	ws, _ := setupWorkspace(t)
	l, err := Open(ws, Options{})
	require.NoError(t, err)
	defer l.Close()
	ctx := context.Background()

	put := func(req engine.SemanticPutRequest) *domain.SemanticSettings {
		t.Helper()
		req.Enabled, req.Kind, req.BaseURL, req.Model, req.Scope = true, "ollama", srv.URL, "nomic-embed-text", "workspace"
		got, err := l.Settings().PutSemantic(ctx, req)
		require.NoError(t, err)
		return got
	}

	first := put(engine.SemanticPutRequest{})
	assert.Equal(t, []string{"operations", "examples", "memories", "docs"}, first.Kinds, "no list means every kind")
	assert.Equal(t, "search_document: ", first.DocumentPrefix, "the prefixes follow the model")
	assert.False(t, first.PrefixesCustom)
	waitForSemanticStats(t, l, func(s semantic.Stats) bool {
		return s.ByKind[semantic.KindOperation] >= 15 && s.ByKind[semantic.KindDoc] > 0
	})

	kinds := []string{"operations", "examples"}
	docPrefix := "passage: "
	second := put(engine.SemanticPutRequest{Kinds: &kinds, DocumentPrefix: &docPrefix})
	assert.Equal(t, kinds, second.Kinds)
	assert.Equal(t, "passage: ", second.DocumentPrefix)
	assert.Equal(t, "search_query: ", second.QueryPrefix, "the side left alone still follows the model")
	assert.True(t, second.PrefixesCustom)
	waitForSemanticStats(t, l, func(s semantic.Stats) bool {
		return s.ByKind[semantic.KindDoc] == 0 && s.ByKind[semantic.KindOperation] >= 15
	})

	status, err := l.SemanticStatus(ctx)
	require.NoError(t, err)
	_, hasDocs := status.ByKind["docs"]
	assert.False(t, hasDocs, "a kind that is off is absent from the status")
	assert.Contains(t, status.ByKind, "operations")

	_, err = l.Examples().Create(ctx, domain.SavedExample{
		ID:          "create-qcom-order",
		Operation:   "order-service.createOrder",
		Description: "Quick-commerce basket placed from the storefront checkout",
		Body: map[string]any{
			"customerId": "cust_123",
			"type":       "QCOM",
			"pickup":     map[string]any{"lat": 12.9716, "lng": 77.5946},
			"drop":       map[string]any{"lat": 12.9352, "lng": 77.6146},
		},
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		_, ok := srv.sawContaining("storefront checkout")
		return ok
	}, 5*time.Second, 25*time.Millisecond, "the example's description is embedded with the operation it calls")
	text, _ := srv.sawContaining("storefront checkout")
	assert.True(t, strings.HasPrefix(text, "passage: "), "with the configured document prefix in front: %q", text)
	assert.Contains(t, text, "Create a new order", "as part of the operation's own text")

	_, err = l.Settings().PutSemantic(ctx, engine.SemanticPutRequest{
		Enabled: true, Kind: "ollama", BaseURL: srv.URL, Model: "nomic-embed-text", Scope: "workspace",
		Kinds: &[]string{"examples"},
	})
	require.Error(t, err, "examples without operations has nothing to ride on")

	reset := put(engine.SemanticPutRequest{ResetPrefixes: true})
	assert.False(t, reset.PrefixesCustom)
	assert.Equal(t, "search_document: ", reset.DocumentPrefix)
}
