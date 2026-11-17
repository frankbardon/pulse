# /// script
# requires-python = "==3.12.*"
# dependencies = [
#   "numpy==2.3.3",
#   "scipy==1.16.2",
#   "scikit-learn==1.7.2",
# ]
# ///
"""Gaussian-process oracle for internal/gp (compose-sweep-2 E4-S1), the
surrogate behind the compose sweep's mode bo.

scikit-learn's GaussianProcessRegressor with normalize_y=True,
alpha = 1e-10 (gp.DefaultNugget) and the kernel

    ConstantKernel(amp) * Matern(length_scale=[...], nu=2.5) + WhiteKernel(noise)

which is internal/gp's model term for term. Per design it records:

  - "fixed": at fixed hyperparameters, the log marginal likelihood and its
    gradient in log-hyperparameters (sklearn's theta order: amplitude,
    length scales, noise) and the posterior mean and TOTAL variance
    (sklearn's predict includes the WhiteKernel noise; internal/gp's
    Predict is the latent variance, so the Go test adds noise * y_std^2);
  - "fitted" (continuous designs only): sklearn's own ML-II fit
    (L-BFGS-B, 1 + 4 restarts, random_state 0) within internal/gp's
    DefaultBounds from its DefaultInitial: the fitted hyperparameters,
    the LML at the optimum and the posterior there.

The "mixed" design has a categorical dimension one-hot encoded into three
columns; internal/gp ties their length scale, so the fixed case expands
the tied value over the three columns (the Go test sums their gradient
entries) and no fitted case is recorded (sklearn cannot tie).

Run: make reference-gp (uv run, pinned deps). Writes
internal/gp/testdata/gp_oracle.json with a // golden-hash: footer; never
hand-edit it.
"""
import hashlib
import json
import sys

import numpy as np
import scipy
import sklearn
from sklearn.gaussian_process import GaussianProcessRegressor
from sklearn.gaussian_process.kernels import ConstantKernel, Matern, WhiteKernel

NUGGET = 1e-10
AMP_BOUNDS = (1e-2, 1e3)
LS_BOUNDS = (1e-2, 1e2)
NOISE_BOUNDS = (1e-6, 1.0)


def kernel(amp, ls, noise, fixed=False):
    b = "fixed" if fixed else None
    return (ConstantKernel(amp, b or AMP_BOUNDS)
            * Matern(length_scale=list(ls), length_scale_bounds=b or LS_BOUNDS, nu=2.5)
            + WhiteKernel(noise, b or NOISE_BOUNDS))


def continuous2():
    x = [[0.05, 0.10], [0.20, 0.85], [0.35, 0.40], [0.50, 0.95], [0.62, 0.15],
         [0.78, 0.60], [0.90, 0.30], [0.12, 0.55], [0.44, 0.70], [0.71, 0.05],
         [0.97, 0.88], [0.28, 0.22]]
    y = [float(np.sin(6.0 * a) + 2.0 * (b - 0.3) ** 2 + 0.5 * a * b) for a, b in x]
    xs = [[0.0, 0.0], [0.33, 0.66], [0.5, 0.5], [0.8, 0.2], [1.0, 1.0], [0.62, 0.15]]
    return {"name": "continuous2", "levels": [0, 0], "x": x, "y": y, "xs": xs,
            "fixed": {"amplitude": 1.7, "length_scales": [0.3, 0.8], "noise": 1e-4},
            "initial": {"amplitude": 1.0, "length_scales": [0.5, 0.5], "noise": 1e-4},
            "fit": True}


def continuous1():
    x = [[v] for v in [0.0, 0.08, 0.21, 0.33, 0.47, 0.52, 0.69, 0.81, 0.93, 1.0]]
    y = [float((v[0] - 0.4) ** 2 * 3.0 - np.cos(9.0 * v[0])) for v in x]
    xs = [[0.15], [0.4], [0.75], [0.99]]
    return {"name": "continuous1", "levels": [0], "x": x, "y": y, "xs": xs,
            "fixed": {"amplitude": 0.6, "length_scales": [0.15], "noise": 3e-3},
            "initial": {"amplitude": 1.0, "length_scales": [0.5], "noise": 1e-4},
            "fit": True}


def mixed():
    # dims: continuous, categorical with 3 levels -> 4 encoded columns.
    pts = [(0.10, 0), (0.30, 1), (0.55, 2), (0.80, 0), (0.95, 1),
           (0.20, 2), (0.45, 0), (0.65, 1), (0.05, 1), (0.75, 2)]
    off = [0.0, 1.5, -0.7]
    x, y = [], []
    for u, k in pts:
        row = [u, 0.0, 0.0, 0.0]
        row[1 + k] = 1.0
        x.append(row)
        y.append(float(np.sin(4.0 * u) + off[k]))
    xs = [[0.5, 1.0, 0.0, 0.0], [0.5, 0.0, 1.0, 0.0], [0.5, 0.0, 0.0, 1.0], [0.9, 0.0, 0.0, 1.0]]
    return {"name": "mixed", "levels": [0, 3], "x": x, "y": y, "xs": xs,
            "fixed": {"amplitude": 0.9, "length_scales": [0.4, 1.3], "noise": 2e-4},
            "expand": [0, 1, 1, 1],
            "fit": False}


def predict(gp, xs):
    m, s = gp.predict(np.array(xs), return_std=True)
    return [float(v) for v in m], [float(v) ** 2 for v in s]


def run(p):
    x, y = np.array(p["x"]), np.array(p["y"])
    expand = p.get("expand", list(range(len(p["levels"]))))
    f = p["fixed"]
    ls = [f["length_scales"][g] for g in expand]
    gp = GaussianProcessRegressor(kernel(f["amplitude"], ls, f["noise"], fixed=True),
                                  alpha=NUGGET, normalize_y=True, optimizer=None).fit(x, y)
    theta = np.log([f["amplitude"]] + ls + [f["noise"]])
    k = kernel(f["amplitude"], ls, f["noise"])
    lml, grad = GaussianProcessRegressor(k, alpha=NUGGET, normalize_y=True, optimizer=None).fit(x, y) \
        .log_marginal_likelihood(theta, eval_gradient=True)
    mean, var = predict(gp, p["xs"])
    out = {k: v for k, v in p.items() if k not in ("fit",)}
    out["y_std"] = float(np.std(y))
    out["fixed_result"] = {"lml": float(lml), "grad": [float(g) for g in grad], "mean": mean, "var": var}
    if p["fit"]:
        i = p["initial"]
        gp = GaussianProcessRegressor(kernel(i["amplitude"], i["length_scales"], i["noise"]),
                                      alpha=NUGGET, normalize_y=True, n_restarts_optimizer=4,
                                      random_state=0).fit(x, y)
        th = np.exp(gp.kernel_.theta)
        mean, var = predict(gp, p["xs"])
        out["fitted_result"] = {
            "amplitude": float(th[0]), "length_scales": [float(v) for v in th[1:-1]],
            "noise": float(th[-1]), "lml": float(gp.log_marginal_likelihood_value_),
            "mean": mean, "var": var,
        }
    return out


def main(path):
    doc = {
        "generator": "scripts/reference/scipy/gen_gp.py",
        "versions": {"numpy": np.__version__, "scipy": scipy.__version__, "scikit-learn": sklearn.__version__},
        "nugget": NUGGET,
        "designs": [run(p) for p in (continuous2(), continuous1(), mixed())],
    }
    body = json.dumps(doc, indent=1) + "\n"
    digest = hashlib.sha256(body.encode()).hexdigest()
    with open(path, "w") as f:
        f.write(body + "\n// golden-hash: " + digest + "\n")


if __name__ == "__main__":
    main(sys.argv[1])
