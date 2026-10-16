# Fit a model with semopy (Model, obj=MLW) and dump estimates + calc_stats as JSON:
#   fit_semopy.py data.csv model.txt [ftol]
import sys, json, time, numpy as np, pandas as pd, semopy
desc = open(sys.argv[2]).read()
df = pd.read_csv(sys.argv[1])
m = semopy.Model(desc)
kw = {"options": {"ftol": float(sys.argv[3]), "maxiter": 100000}} if len(sys.argv) > 3 else {}
t = time.time(); r = m.fit(df, **kw); el = time.time() - t
est = m.inspect(std_est=True)
st = semopy.calc_stats(m).T['Value'].to_dict()
out = {"seconds": el, "fun": float(r.fun), "success": bool(r.success), "n_iter": int(getattr(r, 'n_it', -1)),
       "stats": {k: float(v) for k, v in st.items()},
       "params": [{"lval": a, "op": b, "rval": c, "est": float(e),
                   "std": float(s) if s != '-' else None,
                   "se": None if se == '-' else float(se), "z": None if z == '-' else float(z), "p": None if p == '-' else float(p)}
                  for a, b, c, e, s, se, z, p in est[['lval', 'op', 'rval', 'Estimate', 'Est. Std', 'Std. Err', 'z-value', 'p-value']].itertuples(index=False)]}
json.dump(out, sys.stdout, indent=1)
