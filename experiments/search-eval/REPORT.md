# Static embeddings versus lexical search on the real workspace

Date: 2026-09-05. Question: would built-in static sentence embeddings
(Model2Vec potion-base-2M/4M/8M) improve intent search enough to justify
shipping a few megabytes of model in the binary, compared with the current
FTS5 BM25 plus trigram search?

Short answer: not on their own, and not yet. Embeddings alone are worse
than today's lexical search (Recall@1 0.49 versus 0.72). Fused with lexical
they add about 4 points of Recall@1 and 3.5 of MRR, but three quarters of
that comes from doc text the lexical index can carry itself. Once the
operations index includes doc and memory text, embeddings add roughly 3
points more, concentrated in a few paraphrase queries, and regress some
error-code queries that are perfect today.

## Method

- Data: the user's workspace (169 operations across four services), a copy of its index, and every doc, memory, and example
  under each service's `api/`.
- Queries: `queries.yaml`, 104 intents with verified operation ids, written
  from the docs and contracts: business (30), keyword (27), paraphrase (21),
  error (14), real session intents (6), field (6); 10 flagged as likely
  lexical failures (6 confirmed).
- Systems: current lexical via `sapien search --json`; potion-base-2M/4M/8M
  (reference implementation, cross-checked against a from-scratch
  numpy + WordPiece reimplementation); RRF and weighted fusion; a small BM25
  over doc-enriched operation text to simulate the planned index change;
  section-level embeddings max-pooled to operations.

## Results

| System | R@1 | R@3 | R@5 | MRR |
|---|---|---|---|---|
| Lexical (current) | 0.721 | 0.837 | 0.885 | 0.791 |
| potion-base-2M alone | 0.375 | 0.606 | 0.663 | 0.508 |
| potion-base-4M alone | 0.490 | 0.731 | 0.788 | 0.621 |
| potion-base-8M alone | 0.490 | 0.760 | 0.827 | 0.638 |
| RRF k=60, lexical + 8M | 0.712 | 0.865 | 0.913 | 0.795 |
| Weighted 0.5/0.5, lexical + 8M | 0.760 | 0.894 | 0.904 | 0.826 |
| Doc-enriched BM25 alone | 0.769 | 0.885 | 0.913 | 0.828 |
| Doc-enriched BM25 + 8M, weighted | 0.80 | 0.89 | 0.93 | 0.86 |
| Section embeddings pooled to ops (8M) | 0.288 | 0.558 | 0.673 | 0.444 |

By category, error queries ("why does X happen") are the weakest everywhere
(lexical R@1 0.43) and embeddings make them worse (0.14): mean pooling blurs
the enum-like vocabulary those queries need. Real session intents favour
lexical (0.67 versus 0.17) because they reuse distinctive domain nouns.

## Failure analysis

Six queries lexical missed and embeddings hit: three are also fixed by
doc-enriched BM25 (doc recall, not understanding); three are genuine
paraphrases ("trigger return to origin", "duplicate an order without
cancelling", "cod_not_serviceable"). Twelve queries embeddings missed and
lexical hit, mostly near-verbatim contract language where embeddings rank
a topically adjacent wrong operation first. Weighted fusion improves 17
queries and regresses 7, including "why would automation rule exclusion
happen" from rank 1 to rank 3.

## Cost of the best model (potion-base-8M)

WordPiece tokenizer shared by all three (bge-base-en-v1.5, 29,528 tokens),
MIT licensed. Embedding table 30.2 MB fp32, 7.9 MB int8 with per-row
scale; int8 changes no ranking (bit-identical metrics). Load 2.9 ms, query
0.03 ms. A Go port needs `vocab.txt` (219 KB), the int8 table, and a
greedy longest-match WordPiece tokenizer: an estimated 3 to 5 days
including Unicode edge cases and a byte-for-byte test against the
reference.

## Recommendation

Do not ship a model now. Land the doc and memory text in the operations
index (in progress) and the usage feedback loop, then rerun this harness
(`queries.yaml` is the regression set). If the residual gap is still
wanted, prototype potion-base-8M int8 as an optional weighted re-ranking
pass at 0.5 to 0.7 lexical weight, never always-on, because of the
error-code regressions. Binary size is not the cost; the tokenizer port,
fusion tuning, and maintenance are.

## Reproducing

Scripts and every result JSON live in this directory: `build_docs.py`,
`run_lexical.py`, `run_lexical_scored.py`, `metrics.py`, `embed_rank.py`,
`fusion.py`, `bm25_enriched.py`, `doc_section_embed.py`,
`cost_analysis.py`, `quantization_impact.py`, `failure_analysis.py`.
Models download from Hugging Face (minishlab/potion-base-*) into
`models/`; copy the workspace index to `sapien.db` first; use a venv in
`.venv/` with numpy, tokenizers, model2vec, pyyaml.
