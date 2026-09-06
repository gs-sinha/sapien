"""Compare two lexical result files query by query: which improved, which regressed."""
import json, sys
a = json.load(open(sys.argv[1])); b = json.load(open(sys.argv[2]))
def rank(r):
    v = r.get("rank")
    return v if isinstance(v, int) and v > 0 else 99
A = {r["id"] if "id" in r else r["query"]: r for r in a}
B = {r["id"] if "id" in r else r["query"]: r for r in b}
imp, reg = [], []
cats = {}
for k, ra in A.items():
    rb = B.get(k)
    if rb is None: continue
    x, y = rank(ra), rank(rb)
    c = ra.get("category", "?")
    d = cats.setdefault(c, [0, 0, 0])
    d[0] += 1; d[1] += (x == 1); d[2] += (y == 1)
    if y < x: imp.append((k, x, y, ra.get("query", k)))
    elif y > x: reg.append((k, x, y, ra.get("query", k)))
print(f"improved {len(imp)}  regressed {len(reg)}  unchanged {len(A)-len(imp)-len(reg)}")
print("category: n  R@1 before -> after")
for c, (n, xb, yb) in sorted(cats.items()):
    print(f"  {c:13s} {n:3d}  {xb/n:.3f} -> {yb/n:.3f}")
print("\nIMPROVED (rank before -> after):")
for k, x, y, q in imp[:15]: print(f"  {x:>2} -> {y:<2}  {q}")
print("\nREGRESSED:")
for k, x, y, q in reg[:15]: print(f"  {x:>2} -> {y:<2}  {q}")
