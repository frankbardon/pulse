# Fit a model with semopy to the EXACT biased covariance of the rows
# and dump fit_semopy.py's JSON plus the matrix:
#   fit_semopy_exact.py data.csv model.txt [ftol]
# semopy's own covariance of the rows (utils.cov, numpy) rounds; the
# rows of near_data.csv make the exact one a double, the matrix a
# streaming co-moment reaches, so semopy's cov is swapped for it (rational
# arithmetic, rounded once) — for the fit and for the baseline model
# calc_stats refits from the rows. The rows still feed semopy's
# linregress Lambda starts. It asserts LAPACK dpotrf factors the
# matrix: semopy fits it raw, with no cov_nearest substitution.
import sys, json, numpy as np, pandas as pd, semopy
from fractions import Fraction
from scipy.linalg import lapack
import semopy.model
def exact_cov(x):
    x = [[Fraction(v) for v in r] for r in np.asarray(x, dtype=float)]
    n, p = len(x), len(x[0])
    mean = [sum(r[i] for r in x) / n for i in range(p)]
    return np.array([[float(sum((r[i] - mean[i]) * (r[j] - mean[j]) for r in x) / n) for j in range(p)] for i in range(p)])
semopy.model.cov = exact_cov
desc = open(sys.argv[2]).read()
df = pd.read_csv(sys.argv[1])
m = semopy.Model(desc)
obs = m.vars['observed']
s = exact_cov(df[obs].to_numpy(dtype=float))
n, p = len(df), len(obs)
_, info = lapack.dpotrf(s)
assert info == 0, "the sample covariance does not factor: semopy would substitute"
std = np.sqrt(np.diag(s))
ev = np.linalg.eigvalsh(s / np.outer(std, std))
kw = {"options": {"ftol": float(sys.argv[3]), "maxiter": 100000}} if len(sys.argv) > 3 else {}
r = m.fit(df, **kw)
assert np.array_equal(m.mx_cov, s), "semopy substituted the sample covariance"
est = m.inspect(std_est=True)
st = semopy.calc_stats(m).T['Value'].to_dict()
out = {"fun": float(r.fun), "success": bool(r.success), "n_iter": int(getattr(r, 'n_it', -1)),
       "n": n, "p": p, "min_eigenvalue": float(ev.min()), "sample_cov": s.tolist(), "observed": obs,
       "stats": {k: float(v) for k, v in st.items()},
       "params": [{"lval": a, "op": b, "rval": c, "est": float(e),
                   "std": float(sv) if sv != '-' else None,
                   "se": None if se == '-' else float(se), "z": None if z == '-' else float(z), "p": None if pv == '-' else float(pv)}
                  for a, b, c, e, sv, se, z, pv in est[['lval', 'op', 'rval', 'Estimate', 'Est. Std', 'Std. Err', 'z-value', 'p-value']].itertuples(index=False)]}
json.dump(out, sys.stdout, indent=1)
