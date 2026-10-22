# Fit a model whose sample covariance is not positive definite, as
# semopy does (Model.load_cov's cov_nearest(S, threshold=1e-2)
# fallback), and dump fit_semopy.py's JSON plus the substitution:
#   fit_semopy_nearest.py data.csv model.txt [zscore] [ftol]
# zscore fits the columns z-scored with their population SD, a
# zero-spread column as zeros (VectorSpec.standardize "zscore"). semopy
# cannot fit such a column (its correlation form is NaN), so the
# cov_nearest it calls is wrapped to enter a zero variance at 1, the
# unit variance of the other z-scored columns (a sem.FitCovNearest
# reference only: MAT_SEM refuses a zero-spread member, as semopy
# raises); everything else, the fit from the rows included, is
# semopy's own.
import sys, json, numpy as np, pandas as pd, semopy
from scipy.linalg import lapack
from statsmodels.stats.correlation_tools import cov_nearest
import semopy.model
def unit_zero(cov, **kw):
    cov = np.array(cov, dtype=float); d = np.diag(cov).copy(); d[d == 0] = 1
    np.fill_diagonal(cov, d)
    return cov_nearest(cov, **kw)
semopy.model.cov_nearest = unit_zero
args = sys.argv[1:]
zscore = "zscore" in args
args = [a for a in args if a != "zscore"]
desc = open(args[1]).read()
df = pd.read_csv(args[0])
if zscore:
    sd = df.std(ddof=0)
    df = (df - df.mean()) / sd.where(sd > 0, 1)
    df.loc[:, sd == 0] = 0.0
m = semopy.Model(desc)
obs = m.vars['observed']
x = df[obs].to_numpy(dtype=float); x = x - x.mean(axis=0)
s = x.T @ x / x.shape[0]
n, p = x.shape
_, info = lapack.dpotrf(s)
assert info, "the sample covariance is positive definite: no substitution to test"
flat = np.diag(s) == 0
s1 = s.copy(); s1[flat, flat] = 1
std = np.sqrt(np.diag(s1))
ev = np.linalg.eigvalsh(s1 / np.outer(std, std))
kw = {"options": {"ftol": float(args[2]), "maxiter": 100000}} if len(args) > 2 else {}
r = m.fit(df, **kw)
adj = m.mx_cov
assert not np.array_equal(adj, s), "semopy kept the sample covariance"
assert np.allclose(adj, cov_nearest(s1, threshold=1e-2), rtol=0, atol=1e-12)
est = m.inspect(std_est=True)
st = semopy.calc_stats(m).T['Value'].to_dict()
out = {"fun": float(r.fun), "success": bool(r.success), "n_iter": int(getattr(r, 'n_it', -1)),
       "n": n, "p": p, "min_eigenvalue": float(ev.min()), "sample_cov": s.tolist(), "adjusted_cov": adj.tolist(), "observed": obs,
       "stats": {k: float(v) for k, v in st.items()},
       "params": [{"lval": a, "op": b, "rval": c, "est": float(e),
                   "std": float(sv) if sv != '-' else None,
                   "se": None if se == '-' else float(se), "z": None if z == '-' else float(z), "p": None if pv == '-' else float(pv)}
                  for a, b, c, e, sv, se, z, pv in est[['lval', 'op', 'rval', 'Estimate', 'Est. Std', 'Std. Err', 'z-value', 'p-value']].itertuples(index=False)]}
json.dump(out, sys.stdout, indent=1)
