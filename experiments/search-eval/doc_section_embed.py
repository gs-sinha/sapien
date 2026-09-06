#!/usr/bin/env python3
"""Embed each doc_section (heading + full body) separately with the best
model (potion-base-8M), rank sections per query, then map each section's
similarity back to every operation it references via doc_refs (kind=operation),
taking the max section similarity per operation. Compare against the
operation-level embedding ranking."""
import json
import sqlite3
import numpy as np
import yaml
from model2vec import StaticModel
from metrics import summarize, print_table

BASE = "."
DB = f"{BASE}/sapien.db"
MODEL_DIR = f"{BASE}/models/potion-base-8M"


def main():
    conn = sqlite3.connect(DB)
    conn.row_factory = sqlite3.Row

    sections = {}
    for row in conn.execute("SELECT id, heading, body FROM doc_sections"):
        text = f"{row['heading'] or ''}\n{row['body'] or ''}".strip()
        if text:
            sections[row["id"]] = text

    sec_to_ops = {}
    for row in conn.execute("SELECT section_id, value FROM doc_refs WHERE kind='operation'"):
        sec_to_ops.setdefault(row["section_id"], set()).add(row["value"])

    all_op_ids = [r["id"] for r in conn.execute("SELECT id FROM operations")]

    model = StaticModel.from_pretrained(MODEL_DIR)
    sec_ids = list(sections.keys())
    sec_vecs = np.asarray(model.encode([sections[s] for s in sec_ids], show_progress_bar=False))

    queries = yaml.safe_load(open(f"{BASE}/queries.yaml"))["queries"]
    query_vecs = np.asarray(model.encode([q["text"] for q in queries], show_progress_bar=False))

    sims = query_vecs @ sec_vecs.T  # (n_queries, n_sections)

    results = []
    for qi, q in enumerate(queries):
        op_best_sim = {}
        for si, sec_id in enumerate(sec_ids):
            s = sims[qi, si]
            for opid in sec_to_ops.get(sec_id, ()):
                if opid not in op_best_sim or s > op_best_sim[opid]:
                    op_best_sim[opid] = s
        # ops with no linked doc section at all get -inf (never ranked ahead of any doc-linked op)
        ranked = sorted(op_best_sim.items(), key=lambda x: -x[1])
        ranked_ids = [o for o, _ in ranked]
        expected = set(q["expected_ops"])
        rank = None
        for i, opid in enumerate(ranked_ids[:10], start=1):
            if opid in expected:
                rank = i
                break
        results.append({
            "id": q["id"], "text": q["text"], "category": q["category"],
            "adversarial": bool(q.get("adversarial", False)),
            "expected_ops": q["expected_ops"], "rank": rank,
        })

    summary = summarize(results)
    print_table("Section-level embedding -> max-pooled to operation (potion-base-8M)", summary)
    json.dump(results, open(f"{BASE}/doc_section_embed_results.json", "w"), indent=2)
    json.dump(summary, open(f"{BASE}/doc_section_embed_summary.json", "w"), indent=2)
    print(f"\n{len(sec_ids)} doc sections embedded; "
          f"{sum(1 for o in all_op_ids if any(o in v for v in sec_to_ops.values()))} / {len(all_op_ids)} "
          f"operations reachable via at least one section")


if __name__ == "__main__":
    main()
