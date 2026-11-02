# /// script
# requires-python = "==3.12.*"
# dependencies = [
#   "numpy==2.3.3",
#   "pandas==2.3.3",
#   "scipy==1.16.2",
#   "statsmodels==0.14.5",
# ]
# ///
"""GLM residual oracle for ATTR_REG_RESIDUAL params.kind (U65 E1-S1).

For each canonical family (binomial logit, poisson log, gamma inverse)
it fits statsmodels GLM on a seeded design and writes, per row, the
fitted mean and the four residual kinds:

  response  y - mu                         (R residuals.glm "response")
  pearson   (y - mu) / sqrt(V(mu))         (R "pearson")
  deviance  sign(y - mu) * sqrt(d(y, mu))  (R "deviance")
  working   (y - mu) * g'(mu)              (R "working")

Same in R 4.6.1 (the cross-check to add once R is available):
  m <- glm(y ~ x1 + x2, family = binomial())   # poisson(), Gamma()
  residuals(m, type = "response"|"pearson"|"deviance"|"working")

Run: make reference-statsmodels (uv run, pinned deps). Writes
testdata/statsmodels/glm_residuals.json at %.17g with a
// golden-hash: footer; never hand-edit it.
"""
import hashlib
import json
import sys

import numpy as np
import pandas
import scipy
import statsmodels
import statsmodels.api as sm


def design(rng, n):
    x1 = np.round(rng.normal(0.0, 1.0, n), 3)
    x2 = np.round(rng.uniform(-1.0, 1.0, n), 3)
    return x1, x2


def case(name, family, rng, n, draw):
    x1, x2 = design(rng, n)
    eta = 0.4 + 0.8 * x1 - 0.6 * x2
    y = draw(eta)
    X = sm.add_constant(np.column_stack([x1, x2]))
    res = sm.GLM(y, X, family=family).fit(tol=1e-14, maxiter=200)
    return {
        "name": name,
        "family": {"binomial": "binomial", "poisson": "poisson", "gamma": "gamma"}[name.split("_")[0]],
        "link": {"binomial": "logit", "poisson": "log", "gamma": "inverse"}[name.split("_")[0]],
        "rows": [[float(a), float(b), float(c)] for a, b, c in zip(x1, x2, y)],
        "coefficients": [float(v) for v in res.params],
        "fitted": [float(v) for v in res.fittedvalues],
        "response": [float(v) for v in res.resid_response],
        "pearson": [float(v) for v in res.resid_pearson],
        "deviance": [float(v) for v in res.resid_deviance],
        "working": [float(v) for v in res.resid_working],
    }


def main(out):
    rng = np.random.default_rng(20261101)
    cases = [
        case("binomial_logit", sm.families.Binomial(), rng, 60,
             lambda eta: (rng.uniform(size=eta.size) < 1 / (1 + np.exp(-eta))).astype(float)),
        case("poisson_log", sm.families.Poisson(), rng, 50,
             lambda eta: rng.poisson(np.exp(eta)).astype(float)),
        case("gamma_inverse", sm.families.Gamma(), rng, 50,
             # positive means on the inverse link: mu = 1 / (eta + 2)
             lambda eta: np.round(rng.gamma(4.0, (1 / (np.abs(eta) + 2)) / 4.0), 4)),
    ]
    doc = {
        "generator": "scripts/reference/statsmodels/gen_glm_residuals.py",
        "versions": {"numpy": np.__version__, "pandas": pandas.__version__, "scipy": scipy.__version__, "statsmodels": statsmodels.__version__},
        "cases": cases,
    }
    body = json.dumps(doc, indent=1, default=lambda v: float(v))
    # %.17g round-trip: json.dumps writes repr(float), the shortest exact form.
    body += "\n"
    digest = hashlib.sha256(body.encode()).hexdigest()
    with open(out, "w") as f:
        f.write(body + "\n// golden-hash: " + digest + "\n")


if __name__ == "__main__":
    main(sys.argv[1])
