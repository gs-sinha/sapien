#!/usr/bin/env python3
"""Run every query in queries.yaml through `sapien search --json` and record
the rank of the expected operation(s). Read-only against the live workspace
(sapien CLI resolves the default workspace itself); does not touch the DB.
"""
import json
import subprocess
import sys
import time
import yaml

QUERIES_PATH = "queries.yaml"
OUT_PATH = "lexical_results.json"
LIMIT = 10

import os
if os.environ.get("LEX_FULL"):
    LIMIT = 170
    OUT_PATH = "lexical_results_full.json"


def run_query(text, limit=LIMIT):
    t0 = time.time()
    proc = subprocess.run(
        ["sapien", "search", text, "--json", "--limit", str(limit)],
        cwd="."  # the CLI resolves the default workspace,
        capture_output=True,
        text=True,
        timeout=30,
    )
    elapsed = time.time() - t0
    if proc.returncode != 0:
        print(f"WARN: query failed rc={proc.returncode}: {text!r}\n{proc.stderr[:500]}", file=sys.stderr)
        return [], elapsed
    try:
        data = json.loads(proc.stdout)
    except json.JSONDecodeError:
        print(f"WARN: bad json for {text!r}: {proc.stdout[:300]}", file=sys.stderr)
        return [], elapsed
    op_ids = [r["operation"]["id"] for r in data]
    return op_ids, elapsed


def first_rank(ranked_ids, expected_set):
    for i, opid in enumerate(ranked_ids, start=1):
        if opid in expected_set:
            return i
    return None


def main():
    with open(QUERIES_PATH) as f:
        spec = yaml.safe_load(f)
    queries = spec["queries"]

    results = []
    for q in queries:
        expected = set(q["expected_ops"])
        ranked, elapsed = run_query(q["text"])
        rank = first_rank(ranked, expected)
        results.append({
            "id": q["id"],
            "text": q["text"],
            "category": q["category"],
            "adversarial": bool(q.get("adversarial", False)),
            "expected_ops": q["expected_ops"],
            "ranked_ops": ranked,
            "rank": rank,
            "latency_s": elapsed,
        })
        print(f"{q['id']:6s} rank={str(rank):4s} {elapsed*1000:6.1f}ms  {q['text'][:60]}")

    with open(OUT_PATH, "w") as f:
        json.dump(results, f, indent=2)
    print(f"\nWrote {OUT_PATH}")


if __name__ == "__main__":
    main()
