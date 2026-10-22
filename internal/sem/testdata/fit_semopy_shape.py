# Fit a shape model (shape63_model.txt / shape89_model.txt) to
# shape_data.csv as one MAT_SEM request does — the model's columns, the
# listwise rows, every column z-scored with its population SD (ddof 0;
# VectorSpec.standardize "zscore") — and dump fit_semopy.py's JSON plus
# the rows used and, when semopy substitutes (Model.load_cov's
# cov_nearest when LAPACK dpotrf fails), the substitution:
#   fit_semopy_shape.py shape_data.csv model.txt [rows] [ftol]
# rows (an integer, "all" for every row) keeps only rows with id < rows:
# the n <= p singular case. ftol fits tight (maxiter 100000).
import sys, json, numpy as np, pandas as pd, semopy
from scipy.linalg import lapack
desc = open(sys.argv[2]).read()
rows = sys.argv[3] if len(sys.argv) > 3 else "all"
df = pd.read_csv(sys.argv[1])
if rows != "all":
    df = df[df["id"] < int(rows)]
m = semopy.Model(desc)
obs = m.vars['observed']
df = df[obs].dropna()
sd = df.std(ddof=0)
assert (sd > 0).all(), "a zero-spread column"
df = (df - df.mean()) / sd
x = df.to_numpy(dtype=float); x = x - x.mean(axis=0)
s = x.T @ x / x.shape[0]
n, p = x.shape
_, info = lapack.dpotrf(s)
kw = {"options": {"ftol": float(sys.argv[4]), "maxiter": 100000}} if len(sys.argv) > 4 else {}
r = m.fit(df, **kw)
est = m.inspect(std_est=True)
st = semopy.calc_stats(m).T['Value'].to_dict()
out = {"fun": float(r.fun), "success": bool(r.success), "n_iter": int(getattr(r, 'n_it', -1)),
       "n": n, "p": p, "nearest": bool(info != 0)}
if info:
    std = np.sqrt(np.diag(s))
    out["min_eigenvalue"] = float(np.linalg.eigvalsh(s / np.outer(std, std)).min())
out["stats"] = {k: float(v) for k, v in st.items()}
out["params"] = [{"lval": a, "op": b, "rval": c, "est": float(e),
                  "std": float(sv) if sv != '-' else None,
                  "se": None if se == '-' else float(se), "z": None if z == '-' else float(z), "p": None if pv == '-' else float(pv)}
                 for a, b, c, e, sv, se, z, pv in est[['lval', 'op', 'rval', 'Estimate', 'Est. Std', 'Std. Err', 'z-value', 'p-value']].itertuples(index=False)]
json.dump(out, sys.stdout, indent=1)
