# /// script
# requires-python = "==3.12.*"
# dependencies = [
#   "numpy==2.3.3",
#   "pandas==2.3.3",
#   "scipy==1.16.2",
#   "statsmodels==0.14.5",
# ]
# ///
"""OLS influence oracle for ATTR_REG_RESIDUAL kind standardized /
studentized and ATTR_REG_INFLUENCE measure cooks / dffits (U65 E1-S2).

Each case fits statsmodels OLS with an intercept on a seeded design and
writes, per row, the hat value and the four influence figures of
statsmodels' OLSInfluence:

  standardized  e / (s * sqrt(1 - h))          (R rstandard)
  studentized   e / (s_(i) * sqrt(1 - h))      (R rstudent)
  cooks         r^2 * h / (p' * (1 - h))       (R cooks.distance)
  dffits        t * sqrt(h / (1 - h))          (R dffits)

A frequency-weighted case carries an integer weight per row. Its oracle
is the rep() expansion: the design with each row repeated w times, the
figures read from the row's first copy (one observation of the
expansion; its hat is h / w).

Same in R 4.6.1 (the cross-check to add once R is available):
  m <- lm(y ~ x1 + x2, data = d[rep(seq_len(nrow(d)), d$w), ])
  hatvalues(m); rstandard(m); rstudent(m); cooks.distance(m); dffits(m)

Run: make reference-statsmodels (uv run, pinned deps). Writes
testdata/statsmodels/ols_influence.json with a // golden-hash: footer;
never hand-edit it.
"""
import hashlib
import json
import sys

import numpy as np
import pandas
import scipy
import statsmodels
import statsmodels.api as sm
from statsmodels.stats.outliers_influence import OLSInfluence


def figures(x1, x2, y, w):
    idx = np.repeat(np.arange(len(y)), w)
    X = sm.add_constant(np.column_stack([x1[idx], x2[idx]]))
    res = sm.OLS(y[idx], X).fit()
    inf = OLSInfluence(res)
    first = np.searchsorted(idx, np.arange(len(y)))
    pick = lambda v: [float(v[i]) for i in first]
    return {
        "coefficients": [float(v) for v in res.params],
        "hat": pick(inf.hat_matrix_diag),
        "standardized": pick(inf.resid_studentized_internal),
        "studentized": pick(inf.resid_studentized_external),
        "cooks": pick(inf.cooks_distance[0]),
        "dffits": pick(inf.dffits[0]),
    }


def case(name, rng, n, weighted):
    x1 = np.round(rng.normal(0.0, 1.0, n), 3)
    x2 = np.round(rng.uniform(-1.0, 1.0, n), 3)
    y = np.round(1.5 + 0.8 * x1 - 0.6 * x2 + rng.normal(0.0, 0.5, n), 4)
    # One high-leverage outlier, so the influence figures span a range.
    x1[-1], y[-1] = 4.0, -3.0
    w = rng.integers(1, 4, n) if weighted else np.ones(n, dtype=int)
    out = {"name": name, "weighted": weighted,
           "rows": [[float(a), float(b), float(c), int(d)] for a, b, c, d in zip(x1, x2, y, w)]}
    out.update(figures(x1, x2, y, w))
    return out


def main(out):
    rng = np.random.default_rng(20261102)
    doc = {
        "generator": "scripts/reference/statsmodels/gen_ols_influence.py",
        "versions": {"numpy": np.__version__, "pandas": pandas.__version__, "scipy": scipy.__version__, "statsmodels": statsmodels.__version__},
        "cases": [case("unweighted", rng, 40, False), case("frequency", rng, 30, True)],
    }
    body = json.dumps(doc, indent=1) + "\n"
    digest = hashlib.sha256(body.encode()).hexdigest()
    with open(out, "w") as f:
        f.write(body + "\n// golden-hash: " + digest + "\n")


if __name__ == "__main__":
    main(sys.argv[1])
