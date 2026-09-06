#!/usr/bin/env python3
"""Build one text document per operation: id, method+path, summary, description,
tags, param/field names (reusing the exact tokenized text sapien's own FTS index
uses for those, straight from operations_fts), plus heading + first paragraph of
every doc section that doc_refs links to this operation.
"""
import json
import sqlite3

DB = "sapien.db"
OUT = "operation_docs.json"


def first_paragraph(body, max_chars=600):
    body = body.strip()
    # split on blank line
    parts = body.split("\n\n")
    para = parts[0].strip()
    if len(para) > max_chars:
        para = para[:max_chars]
    return para


def main():
    conn = sqlite3.connect(DB)
    conn.row_factory = sqlite3.Row

    ops = {}
    for row in conn.execute(
        "SELECT id, service, op_id, path_tokens, summary, description, tags, param_names, field_names "
        "FROM operations_fts"
    ):
        ops[row["id"]] = {
            "service": row["service"],
            "op_id_tok": row["op_id"],
            "path_tokens": row["path_tokens"],
            "summary": row["summary"] or "",
            "description": row["description"] or "",
            "tags": row["tags"] or "",
            "param_names": row["param_names"] or "",
            "field_names": row["field_names"] or "",
        }

    # method (not present in FTS row) from operations table
    methods = {}
    for row in conn.execute("SELECT id, method FROM operations"):
        methods[row["id"]] = row["method"]

    # doc snippets: doc_refs(kind='operation') -> section_id -> doc_sections(heading, body)
    sections = {}
    for row in conn.execute("SELECT id, heading, body FROM doc_sections"):
        sections[row["id"]] = (row["heading"] or "", row["body"] or "")

    op_sections = {}
    for row in conn.execute("SELECT section_id, value FROM doc_refs WHERE kind='operation'"):
        op_sections.setdefault(row["value"], []).append(row["section_id"])

    docs_full = {}   # full concatenated text, for the embedding pipeline
    docs_parts = {}  # structured parts, for inspection/debugging

    for opid, o in ops.items():
        method = methods.get(opid, "")
        snippets = []
        for sec_id in op_sections.get(opid, []):
            heading, body = sections.get(sec_id, ("", ""))
            if not body:
                continue
            snippets.append(f"{heading}: {first_paragraph(body)}")
        doc_snippet_text = "\n".join(snippets)

        text = "\n".join([
            opid,
            f"{method} {o['path_tokens']}",
            o["summary"],
            o["description"],
            o["tags"],
            o["param_names"],
            o["field_names"],
            doc_snippet_text,
        ])
        docs_full[opid] = text
        docs_parts[opid] = {
            "service": o["service"],
            "method": method,
            "summary": o["summary"],
            "doc_snippet_text": doc_snippet_text,
            "n_doc_sections": len(op_sections.get(opid, [])),
        }

    with open(OUT, "w") as f:
        json.dump(docs_full, f, indent=2)
    with open(OUT.replace(".json", "_parts.json"), "w") as f:
        json.dump(docs_parts, f, indent=2)

    print(f"Built {len(docs_full)} operation documents -> {OUT}")
    lens = [len(t) for t in docs_full.values()]
    print(f"doc char length: min={min(lens)} max={max(lens)} mean={sum(lens)/len(lens):.0f}")
    with_docs = sum(1 for p in docs_parts.values() if p["n_doc_sections"] > 0)
    print(f"operations with >=1 linked doc section: {with_docs}/{len(docs_parts)}")


if __name__ == "__main__":
    main()
