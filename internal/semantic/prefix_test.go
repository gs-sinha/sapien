package semantic_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/semantic"
)

func TestDefaultPrefixes_Table(t *testing.T) {
	cases := []struct {
		model       string
		query, docs string
	}{
		{"nomic-embed-text", "search_query: ", "search_document: "},
		{"nomic-embed-text:latest", "search_query: ", "search_document: "},
		{"library/nomic-embed-text:v1.5", "search_query: ", "search_document: "},
		{"embeddinggemma", "task: search result | query: ", "title: none | text: "},
		{"mxbai-embed-large", "Represent this sentence for searching relevant passages: ", ""},
		{"bge-m3", "", ""},
		{"all-minilm", "", ""},
		{"text-embedding-3-small", "", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		got := semantic.DefaultPrefixes(tc.model)
		assert.Equal(t, tc.query, got.Query, "query prefix for %q", tc.model)
		assert.Equal(t, tc.docs, got.Document, "document prefix for %q", tc.model)
	}

	qwen := semantic.DefaultPrefixes("qwen3-embedding:0.6b")
	assert.True(t, strings.HasPrefix(qwen.Query, "Instruct: "), "qwen3 takes an instruction on the query")
	assert.True(t, strings.HasSuffix(qwen.Query, "Query: "))
	assert.Empty(t, qwen.Document, "qwen3 embeds documents bare")
}

// recordingEmbedder keeps every text it was asked to embed, so a test can
// see exactly what reached the model.
type recordingEmbedder struct {
	fakeEmbedder
	seen []string
}

func (r *recordingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	r.seen = append(r.seen, texts...)
	return r.fakeEmbedder.Embed(ctx, texts)
}

func TestIndex_PrefixesReachTheModelAndAChangedDocumentPrefixReembeds(t *testing.T) {
	db := openTestDB(t)
	emb := &recordingEmbedder{fakeEmbedder: fakeEmbedder{model: "test-model"}}
	ctx := context.Background()
	ops := []domain.Operation{riderOp()}

	idx := semantic.NewIndex(db, emb).WithPrefixes(semantic.Prefixes{Query: "Q: ", Document: "D: "})
	n, err := idx.IndexOperations(ctx, ops, nil)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Len(t, emb.seen, 1)
	assert.True(t, strings.HasPrefix(emb.seen[0], "D: Find riders"), "document text is prefixed: %q", emb.seen[0])

	_, err = idx.Query(ctx, semantic.KindOperation, "riders near me", 5)
	require.NoError(t, err)
	assert.Equal(t, "Q: riders near me", emb.seen[len(emb.seen)-1])

	// Same prefix again: nothing to do.
	n, err = idx.IndexOperations(ctx, ops, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// A different document prefix is different content for the vector.
	idx2 := semantic.NewIndex(db, emb).WithPrefixes(semantic.Prefixes{Query: "Q: ", Document: "passage: "})
	n, err = idx2.IndexOperations(ctx, ops, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "a changed document prefix re-embeds the row")

	// A changed QUERY prefix alone leaves stored rows alone.
	idx3 := semantic.NewIndex(db, emb).WithPrefixes(semantic.Prefixes{Query: "find: ", Document: "passage: "})
	n, err = idx3.IndexOperations(ctx, ops, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}
