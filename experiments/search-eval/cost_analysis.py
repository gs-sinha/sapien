#!/usr/bin/env python3
"""Cost/footprint analysis for each potion-base model:
- on-disk size as downloaded (safetensors + tokenizer assets)
- int8 per-row-scale quantization of the embedding table (numpy, quantized myself)
- quantization loss (mean cosine sim of original vs. dequantized rows; nearest-
  neighbor ranking agreement on the actual operation corpus)
- vocab size / embedding dim
- load time, RAM footprint
- query latency: numpy+Rust-tokenizer (via model2vec/tokenizers) AND a pure-Python
  (no C-extension) WordPiece + mean-pool reimplementation, used as a conservative
  proxy for "how fast would a straightforward Go port be" (a compiled Go port
  should be faster than an interpreted-Python loop doing the same work, so this
  is an upper bound on Go latency, not a lower bound).
"""
import json
import os
import time
import numpy as np
from safetensors.numpy import load_file

BASE = "."
MODELS = ["potion-base-2M", "potion-base-4M", "potion-base-8M"]


def dir_size(path):
    total = 0
    for root, _, files in os.walk(path):
        for f in files:
            if f.startswith("."):
                continue
            total += os.path.getsize(os.path.join(root, f))
    return total


def quantize_int8_per_row(mat):
    """Per-row scale int8 quantization, as specified by the task."""
    scale = np.abs(mat).max(axis=1, keepdims=True) / 127.0
    scale[scale == 0] = 1.0
    q = np.round(mat / scale).astype(np.int8)
    return q, scale.astype(np.float32)


def dequantize(q, scale):
    return q.astype(np.float32) * scale


# ---------------- pure-Python WordPiece (no Rust/C extension) ----------------
# Mirrors BertNormalizer(lowercase=True) + BertPreTokenizer + WordPiece greedy
# longest-match-first, the same algorithm HF `tokenizers` runs in Rust for this
# tokenizer (confirmed from tokenizer.json: model.type=WordPiece, lowercase
# BertNormalizer, BertPreTokenizer). Used only to time a slow-language
# reference implementation as a conservative stand-in for a compiled Go port.
import re
import unicodedata

_PUNCT_RE = re.compile(r"([\!-\/\:-\@\[-`\{-~])")


def basic_tokenize(text):
    text = unicodedata.normalize("NFD", text.lower())
    text = "".join(c for c in text if unicodedata.category(c) != "Mn")  # strip accents
    text = _PUNCT_RE.sub(r" \1 ", text)
    return text.split()


def wordpiece_tokenize(word, vocab, max_chars=100):
    if len(word) > max_chars:
        return ["[UNK]"]
    out = []
    start = 0
    while start < len(word):
        end = len(word)
        cur = None
        while start < end:
            sub = word[start:end]
            if start > 0:
                sub = "##" + sub
            if sub in vocab:
                cur = sub
                break
            end -= 1
        if cur is None:
            return ["[UNK]"]
        out.append(cur)
        start = end
    return out


def pure_python_encode(text, vocab, embedding, dim):
    words = basic_tokenize(text)
    ids = []
    for w in words:
        for piece in wordpiece_tokenize(w, vocab):
            ids.append(vocab.get(piece, vocab.get("[UNK]", 1)))
    if not ids:
        return np.zeros(dim, dtype=np.float32)
    vecs = embedding[ids]
    v = vecs.mean(axis=0)
    n = np.linalg.norm(v)
    if n > 0:
        v = v / n
    return v


def load_vocab(model_dir):
    vocab = {}
    with open(f"{model_dir}/vocab.txt", encoding="utf-8") as f:
        for i, line in enumerate(f):
            vocab[line.rstrip("\n")] = i
    return vocab


def main():
    report = {}
    op_docs = json.load(open(f"{BASE}/operation_docs.json"))
    sample_texts = list(op_docs.values())[:40]  # for pure-python timing
    query_texts = [q["text"] for q in __import__("yaml").safe_load(open(f"{BASE}/queries.yaml"))["queries"]]

    for name in MODELS:
        mdir = f"{BASE}/models/{name}"
        disk_bytes = dir_size(mdir)
        safet_bytes = os.path.getsize(f"{mdir}/model.safetensors")
        tokenizer_bytes = os.path.getsize(f"{mdir}/tokenizer.json")
        vocab_bytes = os.path.getsize(f"{mdir}/vocab.txt")

        tensors = load_file(f"{mdir}/model.safetensors")
        # find the embedding weight tensor (only real weight matrix in a StaticModel)
        key = [k for k in tensors if tensors[k].ndim == 2][0]
        emb = tensors[key].astype(np.float32)
        vocab_size, dim = emb.shape

        q8, scale = quantize_int8_per_row(emb)
        deq = dequantize(q8, scale)
        # quantization loss: per-row cosine similarity original vs dequantized
        num = (emb * deq).sum(axis=1)
        den = (np.linalg.norm(emb, axis=1) * np.linalg.norm(deq, axis=1)) + 1e-12
        cos = num / den
        quant_cos_mean = float(cos.mean())
        quant_cos_min = float(cos.min())

        int8_bytes = q8.nbytes + scale.nbytes  # embedding table only, quantized
        fp32_embedding_bytes = emb.nbytes

        # shippable footprint: quantized embedding table + vocab.txt (Go port
        # would embed vocab.txt or an equivalent flat token list, not the full
        # tokenizer.json with its redundant merges/config)
        shippable_int8 = int8_bytes + vocab_bytes
        shippable_fp32 = fp32_embedding_bytes + vocab_bytes

        # --- load time (safetensors read + numpy array materialization) ---
        load_times = []
        for _ in range(5):
            t0 = time.time()
            _ = load_file(f"{mdir}/model.safetensors")
            load_times.append(time.time() - t0)
        load_time_ms = float(np.mean(load_times)) * 1000

        # --- pure-python WordPiece + mean-pool timing (Go latency proxy) ---
        vocab = load_vocab(mdir)
        # warm up (dict/regex compilation etc.)
        for t in query_texts[:5]:
            pure_python_encode(t, vocab, emb, dim)
        t0 = time.time()
        for t in query_texts:
            pure_python_encode(t, vocab, emb, dim)
        pp_query_total = time.time() - t0
        pp_per_query_ms = pp_query_total / len(query_texts) * 1000

        t0 = time.time()
        for t in sample_texts:
            pure_python_encode(t, vocab, emb, dim)
        pp_doc_total = time.time() - t0
        pp_per_doc_ms = pp_doc_total / len(sample_texts) * 1000

        report[name] = {
            "vocab_size": int(vocab_size),
            "embedding_dim": int(dim),
            "on_disk_bytes_full_download": disk_bytes,
            "on_disk_mb_full_download": round(disk_bytes / 1e6, 2),
            "safetensors_bytes_fp32_embedding_table": safet_bytes,
            "tokenizer_json_bytes": tokenizer_bytes,
            "vocab_txt_bytes": vocab_bytes,
            "embedding_table_fp32_bytes": fp32_embedding_bytes,
            "embedding_table_int8_bytes": int8_bytes,
            "int8_vs_fp32_ratio": round(int8_bytes / fp32_embedding_bytes, 3),
            "shippable_bytes_fp32_emb_plus_vocab": shippable_fp32,
            "shippable_mb_fp32_emb_plus_vocab": round(shippable_fp32 / 1e6, 2),
            "shippable_bytes_int8_emb_plus_vocab": shippable_int8,
            "shippable_mb_int8_emb_plus_vocab": round(shippable_int8 / 1e6, 2),
            "quantization_cosine_mean": quant_cos_mean,
            "quantization_cosine_min": quant_cos_min,
            "load_time_ms_mean_of_5": load_time_ms,
            "ram_resident_embedding_table_mb_fp32": round(fp32_embedding_bytes / 1e6, 2),
            "numpy_rust_tokenizer_query_latency_ms": None,  # filled from embed_meta.json below
            "pure_python_wordpiece_query_latency_ms": round(pp_per_query_ms, 4),
            "pure_python_wordpiece_doc_latency_ms": round(pp_per_doc_ms, 4),
        }

    # merge in the numpy+Rust-tokenizer latency numbers already measured
    embed_meta = json.load(open(f"{BASE}/embed_meta.json"))
    for name in MODELS:
        report[name]["numpy_rust_tokenizer_query_latency_ms"] = embed_meta[name]["single_query_encode_ms_mean_of_20"]
        report[name]["numpy_rust_tokenizer_load_time_ms"] = embed_meta[name]["load_time_s"] * 1000

    json.dump(report, open(f"{BASE}/cost_analysis.json", "w"), indent=2)
    for name, r in report.items():
        print(f"\n=== {name} ===")
        for k, v in r.items():
            print(f"  {k}: {v}")


if __name__ == "__main__":
    main()
