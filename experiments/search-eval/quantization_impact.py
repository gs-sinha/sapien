#!/usr/bin/env python3
"""Check whether int8 per-row quantization of the embedding table changes
retrieval metrics, for potion-base-8M (the largest/most-precise model, where
any quantization effect would be most visible in absolute embedding count)."""
import json
import numpy as np
from safetensors.numpy import load_file
from tokenizers import Tokenizer
import yaml
from metrics import summarize, print_table

BASE = "."
MODEL = "potion-base-8M"
MDIR = f"{BASE}/models/{MODEL}"


def quantize_int8_per_row(mat):
    scale = np.abs(mat).max(axis=1, keepdims=True) / 127.0
    scale[scale == 0] = 1.0
    q = np.round(mat / scale).astype(np.int8)
    return q, scale.astype(np.float32)


def encode_batch(texts, tok, table):
    encs = tok.encode_batch(texts, add_special_tokens=False)
    dim = table.shape[1]
    out = np.zeros((len(texts), dim), dtype=np.float32)
    for i, e in enumerate(encs):
        ids = e.ids
        if not ids:
            continue
        v = table[ids].mean(axis=0)
        n = np.linalg.norm(v)
        if n > 0:
            v = v / n
        out[i] = v
    return out


def main():
    tensors = load_file(f"{MDIR}/model.safetensors")
    key = [k for k in tensors if tensors[k].ndim == 2][0]
    emb_fp32 = tensors[key].astype(np.float32)
    q8, scale = quantize_int8_per_row(emb_fp32)
    emb_int8_dequant = q8.astype(np.float32) * scale

    tok = Tokenizer.from_file(f"{MDIR}/tokenizer.json")

    op_docs = json.load(open(f"{BASE}/operation_docs.json"))
    doc_ids = list(op_docs.keys())
    doc_texts = [op_docs[d] for d in doc_ids]

    queries = yaml.safe_load(open(f"{BASE}/queries.yaml"))["queries"]
    query_texts = [q["text"] for q in queries]

    for label, table in [("fp32", emb_fp32), ("int8_dequant", emb_int8_dequant)]:
        doc_vecs = encode_batch(doc_texts, tok, table)
        query_vecs = encode_batch(query_texts, tok, table)
        sims = query_vecs @ doc_vecs.T
        results = []
        for qi, q in enumerate(queries):
            order = np.argsort(-sims[qi])
            ranked = [doc_ids[j] for j in order[:10]]
            expected = set(q["expected_ops"])
            rank = None
            for i, opid in enumerate(ranked, start=1):
                if opid in expected:
                    rank = i
                    break
            results.append({"id": q["id"], "text": q["text"], "category": q["category"],
                             "adversarial": bool(q.get("adversarial", False)),
                             "expected_ops": q["expected_ops"], "rank": rank})
        summary = summarize(results)
        print_table(f"{MODEL} [{label}]", summary)
        json.dump(summary, open(f"{BASE}/quant_impact_{label}.json", "w"), indent=2)


if __name__ == "__main__":
    main()
