#!/usr/bin/env python3
"""Like run_lexical.py but keeps the BM25 (fused lexical+trigram) score per
result, full depth, for use in fusion experiments."""
import json
import subprocess
import yaml

BASE = "."


def run_query(text, limit=170):
    proc = subprocess.run(
        ["sapien", "search", text, "--json", "--limit", str(limit)],
        cwd="."  # the CLI resolves the default workspace,
        capture_output=True, text=True, timeout=30,
    )
    data = json.loads(proc.stdout)
    return [(r["operation"]["id"], r["score"]) for r in data]


def main():
    with open(f"{BASE}/queries.yaml") as f:
        queries = yaml.safe_load(f)["queries"]

    out = {}
    for q in queries:
        pairs = run_query(q["text"])
        out[q["id"]] = pairs
        print(q["id"], len(pairs))

    with open(f"{BASE}/lexical_scored.json", "w") as f:
        json.dump(out, f, indent=2)


if __name__ == "__main__":
    main()
