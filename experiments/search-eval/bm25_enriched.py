#!/usr/bin/env python3
"""~60-line BM25 implementation run over operation_docs.json (op id/path/summary/
description/tags/params/fields PLUS the doc-section snippets doc_refs links to
that operation). This simulates the "doc snippets folded into FTS" improvement
another agent is making to the lexical index, so we can check whether static
embeddings' gains are really paraphrase understanding or just doc-text recall
that lexical search is about to get anyway.

Tokenizer mirrors what operations_fts already contains (see build_docs.py):
words split on non-alnum, plus camelCase/snake_case sub-splitting, matching
Sapien's own path/op-id tokenization convention (PLAN.md #16).
"""
import json
import math
import re
from collections import Counter

BASE = "."

WORD_RE = re.compile(r"[A-Za-z0-9]+")
CAMEL_RE = re.compile(r"(?<=[a-z0-9])(?=[A-Z])|(?<=[A-Z])(?=[A-Z][a-z])")


def tokenize(text):
    toks = []
    for raw in WORD_RE.findall(text):
        low = raw.lower()
        toks.append(low)
        # split camelCase / snake_case-derived words into sub-tokens too,
        # same idea as Sapien's path/op_id tokenizer (PLAN.md #16)
        parts = [p.lower() for p in CAMEL_RE.split(raw) if p]
        if len(parts) > 1:
            toks.extend(parts)
    return toks


class BM25:
    def __init__(self, doc_ids, doc_texts, k1=1.5, b=0.75):
        self.doc_ids = doc_ids
        self.k1, self.b = k1, b
        self.doc_tokens = [tokenize(t) for t in doc_texts]
        self.doc_len = [len(d) for d in self.doc_tokens]
        self.avgdl = sum(self.doc_len) / len(self.doc_len)
        self.tf = [Counter(d) for d in self.doc_tokens]
        df = Counter()
        for d in self.doc_tokens:
            df.update(set(d))
        n = len(doc_ids)
        self.idf = {term: math.log(1 + (n - c + 0.5) / (c + 0.5)) for term, c in df.items()}

    def score_all(self, query):
        q_tokens = tokenize(query)
        scores = [0.0] * len(self.doc_ids)
        for i, tf in enumerate(self.tf):
            dl = self.doc_len[i]
            s = 0.0
            for term in q_tokens:
                if term not in tf:
                    continue
                f = tf[term]
                idf = self.idf.get(term, 0.0)
                s += idf * f * (self.k1 + 1) / (f + self.k1 * (1 - self.b + self.b * dl / self.avgdl))
            scores[i] = s
        return scores

    def rank(self, query):
        scores = self.score_all(query)
        order = sorted(range(len(self.doc_ids)), key=lambda i: -scores[i])
        return [(self.doc_ids[i], scores[i]) for i in order]


def main():
    import yaml
    from metrics import summarize, print_table

    with open(f"{BASE}/operation_docs.json") as f:
        op_docs = json.load(f)
    doc_ids = list(op_docs.keys())
    doc_texts = [op_docs[d] for d in doc_ids]
    bm25 = BM25(doc_ids, doc_texts)

    with open(f"{BASE}/queries.yaml") as f:
        queries = yaml.safe_load(f)["queries"]

    results = []
    full_rankings = {}
    for q in queries:
        ranked = bm25.rank(q["text"])
        full_rankings[q["id"]] = ranked
        expected = set(q["expected_ops"])
        rank = None
        for i, (opid, _) in enumerate(ranked[:10], start=1):
            if opid in expected:
                rank = i
                break
        results.append({
            "id": q["id"], "text": q["text"], "category": q["category"],
            "adversarial": bool(q.get("adversarial", False)),
            "expected_ops": q["expected_ops"], "rank": rank,
        })

    summary = summarize(results)
    print_table("Doc-enriched BM25 (own impl, ops+doc-snippets)", summary)
    json.dump(results, open(f"{BASE}/bm25_enriched_results.json", "w"), indent=2)
    json.dump(summary, open(f"{BASE}/bm25_enriched_summary.json", "w"), indent=2)
    json.dump({qid: r for qid, r in full_rankings.items()}, open(f"{BASE}/bm25_enriched_full_rankings.json", "w"), indent=2)


if __name__ == "__main__":
    main()
