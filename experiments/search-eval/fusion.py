#!/usr/bin/env python3
"""RRF and weighted fusion of the lexical baseline with each embedding model,
full-depth ranking on both sides. Reports the same Recall@k/MRR metrics."""
import json
import numpy as np
import yaml
from metrics import summarize, print_table

BASE = "."
MODELS = ["potion-base-2M", "potion-base-4M", "potion-base-8M"]
RRF_K = 60


def load_queries():
    with open(f"{BASE}/queries.yaml") as f:
        return {q["id"]: q for q in yaml.safe_load(f)["queries"]}


def load_lexical_scored():
    with open(f"{BASE}/lexical_scored.json") as f:
        return json.load(f)  # qid -> [[opid, score], ...] already ranked desc


def load_embedding_full(model_name):
    doc_ids = json.load(open(f"{BASE}/doc_ids_{model_name}.json"))
    doc_vecs = np.load(f"{BASE}/doc_vecs_{model_name}.npy")
    query_vecs = np.load(f"{BASE}/query_vecs_{model_name}.npy")
    return doc_ids, doc_vecs, query_vecs


def ranked_with_scores(sims_row, doc_ids):
    order = np.argsort(-sims_row)
    return [(doc_ids[j], float(sims_row[j])) for j in order]


def rrf_combine(lex_ranked, emb_ranked, k=RRF_K):
    """lex_ranked / emb_ranked: list of (opid, score) already sorted desc by score."""
    lex_rank = {opid: i + 1 for i, (opid, _) in enumerate(lex_ranked)}
    emb_rank = {opid: i + 1 for i, (opid, _) in enumerate(emb_ranked)}
    all_ids = set(lex_rank) | set(emb_rank)
    fused = []
    for opid in all_ids:
        s = 0.0
        if opid in lex_rank:
            s += 1.0 / (k + lex_rank[opid])
        if opid in emb_rank:
            s += 1.0 / (k + emb_rank[opid])
        fused.append((opid, s))
    fused.sort(key=lambda x: -x[1])
    return fused


def zscore(scores):
    arr = np.array(scores, dtype=float)
    mu, sd = arr.mean(), arr.std()
    if sd < 1e-9:
        return np.zeros_like(arr)
    return (arr - mu) / sd


def weighted_combine(lex_ranked, emb_ranked, w_lex=0.5, w_emb=0.5):
    lex_ids = [o for o, _ in lex_ranked]
    lex_scores = zscore([s for _, s in lex_ranked])
    lex_z = dict(zip(lex_ids, lex_scores))

    emb_ids = [o for o, _ in emb_ranked]
    emb_scores = zscore([s for _, s in emb_ranked])
    emb_z = dict(zip(emb_ids, emb_scores))

    all_ids = set(lex_z) | set(emb_z)
    # missing side gets the minimum z-score of that side (worst case, not zero/mean)
    lex_min = min(lex_z.values()) if lex_z else 0.0
    emb_min = min(emb_z.values()) if emb_z else 0.0
    fused = []
    for opid in all_ids:
        s = w_lex * lex_z.get(opid, lex_min) + w_emb * emb_z.get(opid, emb_min)
        fused.append((opid, s))
    fused.sort(key=lambda x: -x[1])
    return fused


def rank_of_expected(fused, expected_set, topn=10):
    for i, (opid, _) in enumerate(fused[:topn], start=1):
        if opid in expected_set:
            return i
    return None


def evaluate(fuse_fn, queries, lexical_scored, model_name, **kwargs):
    doc_ids, doc_vecs, query_vecs = load_embedding_full(model_name)
    sims = query_vecs @ doc_vecs.T
    results = []
    for qi, (qid, q) in enumerate(queries.items()):
        lex_ranked = [(o, s) for o, s in lexical_scored[qid]]
        emb_ranked = ranked_with_scores(sims[qi], doc_ids)
        fused = fuse_fn(lex_ranked, emb_ranked, **kwargs)
        expected = set(q["expected_ops"])
        rank = rank_of_expected(fused, expected)
        results.append({
            "id": qid, "text": q["text"], "category": q["category"],
            "adversarial": bool(q.get("adversarial", False)),
            "expected_ops": q["expected_ops"], "rank": rank,
        })
    return results


def main():
    queries = load_queries()
    lexical_scored = load_lexical_scored()
    # keep query dict order matching lexical_scored's qid order for indexing into sims
    ordered_queries = {qid: queries[qid] for qid in lexical_scored}

    all_summaries = {}
    for model_name in MODELS:
        print(f"\n########## RRF fusion (k={RRF_K}) : lexical + {model_name} ##########")
        results = evaluate(rrf_combine, ordered_queries, lexical_scored, model_name)
        summary = summarize(results)
        print_table(f"RRF lexical+{model_name}", summary)
        all_summaries[f"rrf_{model_name}"] = summary
        json.dump(results, open(f"{BASE}/fusion_rrf_{model_name}.json", "w"), indent=2)

    # weighted fusion: try a couple of weight settings on the best model (8M)
    best_model = "potion-base-8M"
    for w_lex, w_emb in [(0.5, 0.5), (0.7, 0.3), (0.3, 0.7)]:
        print(f"\n########## Weighted fusion w_lex={w_lex} w_emb={w_emb} : lexical + {best_model} ##########")
        results = evaluate(weighted_combine, ordered_queries, lexical_scored, best_model, w_lex=w_lex, w_emb=w_emb)
        summary = summarize(results)
        print_table(f"Weighted({w_lex}/{w_emb}) lexical+{best_model}", summary)
        all_summaries[f"weighted_{w_lex}_{w_emb}_{best_model}"] = summary
        json.dump(results, open(f"{BASE}/fusion_weighted_{w_lex}_{w_emb}.json", "w"), indent=2)

    json.dump(all_summaries, open(f"{BASE}/fusion_summaries.json", "w"), indent=2)


if __name__ == "__main__":
    main()
