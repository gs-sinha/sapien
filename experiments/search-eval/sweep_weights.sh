#!/bin/sh
# Sweep the doc_text/memory_text bm25 weights via SAPIEN_SEARCH_KNOWLEDGE_WEIGHTS
# and report overall metrics plus improved/regressed counts against the baseline.
for w in "0,0" "1,1" "2,2" "3,3" "4,4" "6,6" "8,8" "4,1" "1,4"; do
  SAPIEN_SEARCH_KNOWLEDGE_WEIGHTS="$w" .venv/bin/python run_lexical.py >/dev/null 2>&1
  cp lexical_results.json "lexical_results_w${w}.json"
  .venv/bin/python - "$w" <<'PY'
import json, sys
w=sys.argv[1]
rows=json.load(open('lexical_results.json')); base={r['id']:r for r in json.load(open('lexical_results_baseline.json'))}
def rk(r):
    v=r.get('rank'); return v if isinstance(v,int) and v>0 else 99
n=len(rows); r1=sum(rk(r)==1 for r in rows)/n; r3=sum(rk(r)<=3 for r in rows)/n; r5=sum(rk(r)<=5 for r in rows)/n
mrr=sum((1/rk(r)) if rk(r)<99 else 0 for r in rows)/n
imp=sum(rk(r)<rk(base[r['id']]) for r in rows); reg=sum(rk(r)>rk(base[r['id']]) for r in rows)
miss=sum(rk(r)==99 for r in rows)
print(f"weights {w:>4}: R@1={r1:.3f} R@3={r3:.3f} R@5={r5:.3f} MRR={mrr:.3f} miss={miss:2d}  improved={imp:2d} regressed={reg:2d}")
PY
done
