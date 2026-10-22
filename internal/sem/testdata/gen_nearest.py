# statsmodels' cov_nearest(S, threshold=1e-2) — the substitution semopy's
# Model.load_cov makes for a sample covariance that is not positive
# definite — on synthetic matrices, the reference for sem.CovNearest:
#   gen_nearest.py > nearest_statsmodels.json
import json, sys, numpy as np
from statsmodels.stats.correlation_tools import cov_nearest
rng = np.random.default_rng(23)
def biased(x):
    x = x - x.mean(axis=0)
    return x.T @ x / x.shape[0]
cases = {}
# Fewer rows than columns, columns on different scales: rank n - 1.
cases["rank_deficient"] = biased(rng.normal(size=(5, 7)) * np.array([1, 2, 0.5, 10, 3, 1, 0.1]))
# A symmetric matrix with a negative eigenvalue (an inconsistent
# pairwise-style input), variances away from 1.
c = np.array([[1, .9, .9, -.2], [.9, 1, -.3, .5], [.9, -.3, 1, .4], [-.2, .5, .4, 1]])
sd = np.array([2, .5, 1.5, 3])
cases["indefinite"] = c * np.outer(sd, sd)
# Positive definite with every correlation-form eigenvalue above the
# threshold: returned through the correlation form unclipped.
cases["unclipped"] = biased(rng.normal(size=(40, 4)) * np.array([1, 3, 0.2, 5]))
# A correlation matrix with an exact dependency (z-scored columns, one a
# sum of two others) and a member at unit variance uncorrelated with
# the rest (a zero-spread member raised to unit variance).
z = rng.normal(size=(12, 4)); z[:, 3] = z[:, 0] + z[:, 1]
z = (z - z.mean(axis=0)) / z.std(axis=0)
r = np.zeros((5, 5)); r[:4, :4] = biased(z); r[4, 4] = 1
cases["dependent_unit_member"] = r
out = {}
for name, s in cases.items():
    std = np.sqrt(np.diag(s))
    ev = np.linalg.eigvalsh(s / np.outer(std, std))
    out[name] = {"input": s.tolist(), "output": cov_nearest(s, threshold=1e-2).tolist(),
                 "min_eigenvalue": float(ev.min()), "clipped": bool((ev < 1e-2).any())}
json.dump(out, sys.stdout, indent=1)
