# internal/semantic

Optional, off-by-default semantic search (PLAN.md §16). Embeds operations,
doc sections, and memories into the `vectors` table; queries answer with
brute-force cosine similarity — no cgo, no sqlite-vec. `internal/search`
fuses these hits with lexical FTS5 results via reciprocal-rank fusion when
`Searcher.WithSemantic` gets a non-nil implementation.

## Enabling it

Settings keys the engine reads to build an `HTTPConfig` for
`NewHTTPEmbedder`:

| key | meaning |
|---|---|
| `semantic.kind` | `"openai"` or `"ollama"` |
| `semantic.base_url` | endpoint base, e.g. `http://localhost:11434` |
| `semantic.model` | embedding model name |
| `semantic.api_key` | literal key, `${env.NAME}`/`$NAME` for an env var, or omit (Ollama) |

Leaving `semantic.kind` unset means no `Embedder` is built, `WithSemantic` is
never called, and search stays lexical-only.

## Ollama example

`ollama pull nomic-embed-text`, then:
`kind=ollama base_url=http://localhost:11434 model=nomic-embed-text`

## OpenAI-compatible example

`kind=openai base_url=https://api.openai.com/v1 model=text-embedding-3-small api_key=${env.OPENAI_API_KEY}`

## Limits

- Brute-force cosine over every row of the queried kind: fine to roughly
  10,000 rows per kind, no index beyond that.
- Rows only compare within the embedder's current `(model, dim)`; a model
  switch leaves old rows stored but ignored until re-indexed.
- Indexing is explicit/lazy — `catalog.Apply` never calls this package; a
  caller decides when to run `IndexOperations`/`IndexDocs`/`IndexMemories`.
