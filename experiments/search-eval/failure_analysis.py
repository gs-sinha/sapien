#!/usr/bin/env python3
"""Queries where lexical and best-embedding disagree, and where doc-enriched
BM25 already recovers what plain embeddings seemed to add."""
import json

BASE = "."


def load(name):
    return {r["id"]: r for r in json.load(open(f"{BASE}/{name}"))}


def hit(r, k=5):
    return r["rank"] is not None and r["rank"] <= k


def main():
    lex = load("lexical_results.json")
    emb = load("embed_results_potion-base-8M.json")
    fused = load("fusion_weighted_0.5_0.5.json")
    docbm25 = load("bm25_enriched_results.json")

    ids = list(lex.keys())

    lex_wrong_emb_right = []
    emb_wrong_lex_right = []
    both_wrong = []
    docbm25_recovers = []  # lexical wrong (current), but doc-enriched bm25 alone gets it right
    fusion_helps_over_lex = []
    fusion_hurts_vs_lex = []

    for qid in ids:
        l, e, f, d = lex[qid], emb[qid], fused[qid], docbm25[qid]
        if not hit(l) and hit(e):
            lex_wrong_emb_right.append((qid, l["text"], l["rank"], e["rank"]))
        if hit(l) and not hit(e):
            emb_wrong_lex_right.append((qid, l["text"], l["rank"], e["rank"]))
        if not hit(l) and not hit(e):
            both_wrong.append((qid, l["text"], l["rank"], e["rank"], d["rank"]))
        if not hit(l) and hit(d):
            docbm25_recovers.append((qid, l["text"], l["rank"], d["rank"]))
        if (f["rank"] or 99) < (l["rank"] or 99):
            fusion_helps_over_lex.append((qid, l["text"], l["rank"], f["rank"]))
        if (f["rank"] or 99) > (l["rank"] or 99):
            fusion_hurts_vs_lex.append((qid, l["text"], l["rank"], f["rank"]))

    report = {
        "lexical_wrong_embedding_right (rank<=5)": lex_wrong_emb_right,
        "embedding_wrong_lexical_right (rank<=5)": emb_wrong_lex_right,
        "both_wrong_top5 (lex_rank, emb_rank, docbm25_rank)": both_wrong,
        "current_lexical_wrong_but_doc_enriched_bm25_right": docbm25_recovers,
        "fusion_improves_rank_vs_lexical": fusion_helps_over_lex,
        "fusion_worsens_rank_vs_lexical": fusion_hurts_vs_lex,
    }
    for k, v in report.items():
        print(f"\n=== {k} ({len(v)}) ===")
        for row in v:
            print(" ", row)

    json.dump(report, open(f"{BASE}/failure_analysis.json", "w"), indent=2)


if __name__ == "__main__":
    main()
