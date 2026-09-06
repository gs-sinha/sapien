#!/usr/bin/env python3
"""Embed operation documents and queries with each potion-base model, rank by
cosine similarity, and compute the same rank/metrics structure as the lexical
baseline. Also times model load and per-query encode latency.
"""
import json
import time
import numpy as np
import yaml
from model2vec import StaticModel

BASE = "."
MODELS = {
    "potion-base-2M": f"{BASE}/models/potion-base-2M",
    "potion-base-4M": f"{BASE}/models/potion-base-4M",
    "potion-base-8M": f"{BASE}/models/potion-base-8M",
}


def load_queries():
    with open(f"{BASE}/queries.yaml") as f:
        return yaml.safe_load(f)["queries"]


def cosine_rank(query_vecs, doc_vecs, doc_ids):
    # doc_vecs assumed L2-normalized already (potion models normalize=true)
    sims = query_vecs @ doc_vecs.T  # (n_queries, n_docs)
    order = np.argsort(-sims, axis=1)
    ranked = [[doc_ids[j] for j in order[i]] for i in range(len(query_vecs))]
    return ranked, sims


def run_model(model_name, model_path, op_docs, queries):
    t0 = time.time()
    model = StaticModel.from_pretrained(model_path)
    load_time = time.time() - t0

    doc_ids = list(op_docs.keys())
    doc_texts = [op_docs[d] for d in doc_ids]

    t0 = time.time()
    doc_vecs = model.encode(doc_texts, show_progress_bar=False)
    doc_encode_time = time.time() - t0

    query_texts = [q["text"] for q in queries]
    t0 = time.time()
    query_vecs = model.encode(query_texts, show_progress_bar=False)
    query_encode_total = time.time() - t0
    per_query_latency = query_encode_total / len(query_texts)

    # also time a single cold single-query encode (batch=1) for a realistic
    # "one query at a time" latency number
    single_times = []
    for qt in query_texts[:20]:
        t0 = time.time()
        model.encode([qt], show_progress_bar=False)
        single_times.append(time.time() - t0)
    single_query_latency = float(np.mean(single_times))

    ranked_lists, sims = cosine_rank(np.asarray(query_vecs), np.asarray(doc_vecs), doc_ids)

    results = []
    for q, ranked in zip(queries, ranked_lists):
        expected = set(q["expected_ops"])
        rank = None
        for i, opid in enumerate(ranked[:10], start=1):
            if opid in expected:
                rank = i
                break
        results.append({
            "id": q["id"],
            "text": q["text"],
            "category": q["category"],
            "adversarial": bool(q.get("adversarial", False)),
            "expected_ops": q["expected_ops"],
            "ranked_ops": ranked[:10],
            "rank": rank,
        })

    meta = {
        "model": model_name,
        "load_time_s": load_time,
        "n_docs": len(doc_ids),
        "doc_encode_time_s": doc_encode_time,
        "doc_encode_per_doc_ms": doc_encode_time / len(doc_ids) * 1000,
        "query_batch_encode_per_query_ms": per_query_latency * 1000,
        "single_query_encode_ms_mean_of_20": single_query_latency * 1000,
        "dim": int(np.asarray(doc_vecs).shape[1]),
    }
    return results, meta, doc_vecs, doc_ids, query_vecs


def main():
    queries = load_queries()
    with open(f"{BASE}/operation_docs.json") as f:
        op_docs = json.load(f)

    all_meta = {}
    for name, path in MODELS.items():
        print(f"\n=== {name} ===")
        results, meta, doc_vecs, doc_ids, query_vecs = run_model(name, path, op_docs, queries)
        with open(f"{BASE}/embed_results_{name}.json", "w") as f:
            json.dump(results, f, indent=2)
        np.save(f"{BASE}/doc_vecs_{name}.npy", np.asarray(doc_vecs))
        np.save(f"{BASE}/query_vecs_{name}.npy", np.asarray(query_vecs))
        with open(f"{BASE}/doc_ids_{name}.json", "w") as f:
            json.dump(doc_ids, f)
        all_meta[name] = meta
        print(json.dumps(meta, indent=2))

    with open(f"{BASE}/embed_meta.json", "w") as f:
        json.dump(all_meta, f, indent=2)


if __name__ == "__main__":
    main()
