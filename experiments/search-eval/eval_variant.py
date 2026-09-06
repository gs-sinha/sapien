import json, sys
label=sys.argv[1]
rows=json.load(open('lexical_results.json')); base={r['id']:r for r in json.load(open('lexical_results_baseline.json'))}
def rk(r):
    v=r.get('rank'); return v if isinstance(v,int) and v>0 else 99
n=len(rows); r1=sum(rk(r)==1 for r in rows)/n; r3=sum(rk(r)<=3 for r in rows)/n; r5=sum(rk(r)<=5 for r in rows)/n
mrr=sum((1/rk(r)) if rk(r)<99 else 0 for r in rows)/n
imp=sum(rk(r)<rk(base[r['id']]) for r in rows); reg=sum(rk(r)>rk(base[r['id']]) for r in rows)
cats={}
for r in rows:
    c=r['category']; d=cats.setdefault(c,[0,0,0]); d[0]+=1; d[1]+=rk(base[r['id']])==1; d[2]+=rk(r)==1
worst=min((y-x)/n_ for c,(n_,x,y) in cats.items())
print(f"{label:>22}: R@1={r1:.3f} R@3={r3:.3f} R@5={r5:.3f} MRR={mrr:.3f}  improved={imp:2d} regressed={reg:2d}  worst-cat dR@1={worst:+.3f}")
