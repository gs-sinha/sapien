package semantic

import "strings"

// Prefixes are the task prefixes an embedding model was trained with: most
// retrieval models embed a query and a document differently and tell the
// two apart by a short instruction in front of the text. Sent bare, such a
// model still answers, but with vectors from the wrong half of its training
// -- quietly worse retrieval, never an error.
type Prefixes struct {
	// Query goes in front of the text of a search.
	Query string
	// Document goes in front of every indexed text. It is part of what the
	// index hashes, so changing it re-embeds every row on the next pass.
	Document string
}

// DefaultPrefixes returns the prefixes model's own documentation asks for,
// matched on the model's base name (any ":tag" and any "org/" ignored), and
// none for a model this table does not know -- which is also right for the
// models that were trained without any (bge-m3, all-minilm, OpenAI's
// text-embedding-3). A workspace overrides either side in its semantic:
// config (query_prefix, document_prefix).
func DefaultPrefixes(model string) Prefixes {
	name := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[:i]
	}
	switch {
	case strings.HasPrefix(name, "nomic-embed-text"):
		return Prefixes{Query: "search_query: ", Document: "search_document: "}
	case strings.HasPrefix(name, "embeddinggemma"):
		return Prefixes{Query: "task: search result | query: ", Document: "title: none | text: "}
	case strings.HasPrefix(name, "qwen3-embedding"):
		return Prefixes{Query: "Instruct: Given a search query, retrieve the API operations, documentation and notes that answer it\nQuery: "}
	case strings.HasPrefix(name, "mxbai-embed"):
		return Prefixes{Query: "Represent this sentence for searching relevant passages: "}
	case strings.HasPrefix(name, "snowflake-arctic-embed"):
		return Prefixes{Query: "Represent this sentence for searching relevant passages: "}
	}
	return Prefixes{}
}
