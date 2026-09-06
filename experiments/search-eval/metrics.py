#!/usr/bin/env python3
"""Shared metrics helpers: Recall@k / MRR, overall and by category/adversarial flag."""
import json
from collections import defaultdict


def load_queries():
    import yaml
    with open("queries.yaml") as f:
        return yaml.safe_load(f)["queries"]


def recall_at_k(rank, k):
    return 1.0 if (rank is not None and rank <= k) else 0.0


def rr(rank):
    return 1.0 / rank if rank is not None else 0.0


def summarize(results, group_keys=("category",)):
    """results: list of dicts with at least 'rank' and the group_keys fields (plus 'adversarial')."""
    def agg(rows):
        n = len(rows)
        if n == 0:
            return {}
        r1 = sum(recall_at_k(r["rank"], 1) for r in rows) / n
        r3 = sum(recall_at_k(r["rank"], 3) for r in rows) / n
        r5 = sum(recall_at_k(r["rank"], 5) for r in rows) / n
        mrr = sum(rr(r["rank"]) for r in rows) / n
        misses = sum(1 for r in rows if r["rank"] is None)
        return {"n": n, "recall@1": r1, "recall@3": r3, "recall@5": r5, "mrr": mrr, "not_found_in_top10": misses}

    out = {"overall": agg(results)}
    for key in group_keys:
        groups = defaultdict(list)
        for r in results:
            groups[r[key]].append(r)
        out[key] = {str(k): agg(v) for k, v in sorted(groups.items())}
    adv = [r for r in results if r.get("adversarial")]
    nonadv = [r for r in results if not r.get("adversarial")]
    out["adversarial=true"] = agg(adv)
    out["adversarial=false"] = agg(nonadv)
    return out


def print_table(name, summary):
    print(f"\n=== {name}: overall ===")
    o = summary["overall"]
    print(f"n={o['n']}  R@1={o['recall@1']:.3f}  R@3={o['recall@3']:.3f}  R@5={o['recall@5']:.3f}  MRR={o['mrr']:.3f}  not_in_top10={o['not_found_in_top10']}")
    for key in summary:
        if key in ("overall",):
            continue
        if key.startswith("adversarial"):
            continue
        print(f"\n--- by {key} ---")
        for k, v in summary[key].items():
            print(f"  {k:16s} n={v['n']:3d}  R@1={v['recall@1']:.3f}  R@3={v['recall@3']:.3f}  R@5={v['recall@5']:.3f}  MRR={v['mrr']:.3f}")
    print("\n--- adversarial split ---")
    for k in ("adversarial=true", "adversarial=false"):
        v = summary[k]
        print(f"  {k:18s} n={v['n']:3d}  R@1={v['recall@1']:.3f}  R@3={v['recall@3']:.3f}  R@5={v['recall@5']:.3f}  MRR={v['mrr']:.3f}")


if __name__ == "__main__":
    with open("lexical_results.json") as f:
        results = json.load(f)
    summary = summarize(results)
    print_table("Lexical baseline", summary)
    with open("lexical_summary.json", "w") as f:
        json.dump(summary, f, indent=2)
