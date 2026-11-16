# /// script
# requires-python = "==3.12.*"
# dependencies = [
#   "numpy==2.3.3",
#   "scipy==1.16.2",
#   "scikit-learn==1.7.2",
#   "scikit-optimize==0.10.2",
# ]
# ///
"""Bayesian-optimisation oracle for the compose sweep's mode bo
(compose-sweep-2 E4-S2, internal/sweep/bo.go).

scikit-optimize's gp_minimize with the settings mode bo runs: a
Latin-hypercube initial design of n_init points
(initial_point_generator="lhs"), expected-improvement acquisition
with no exploration margin (acq_func="EI", xi=0, as mode bo) maximised
over 1000 sampled candidates with 5 local polishes
(acq_optimizer="lbfgs", n_points=1000, n_restarts_optimizer=5), one
point a round, over its default surrogate
— ConstantKernel * Matern(nu=2.5, ARD) + WhiteKernel with normalize_y,
internal/gp's model term for term — for exactly `budget` evaluations.

The two engines draw from different random streams (and skopt polishes
by L-BFGS-B where mode bo runs SLSQP, and fits its surrogate from other
restarts), so their trajectories cannot match; the oracle pins the OUTCOME instead: per
seed, the best point and value gp_minimize reaches on the same problem
and budget. The Go test (internal/sweep/bo_oracle_heavy_test.go) runs
mode bo over as many seeds and compares the distributions.

Run: make reference-sweep-bo (uv run, pinned deps). Writes
internal/sweep/testdata/bo_oracle.json with a // golden-hash: footer;
never hand-edit it.
"""
import hashlib
import json
import math
import sys

import numpy as np
import scipy
import sklearn
import skopt
from skopt import gp_minimize


def branin(x):
    b = 5.1 / (4 * math.pi ** 2)
    c = 5 / math.pi
    t = 1 / (8 * math.pi)
    d = x[1] - b * x[0] ** 2 + c * x[0] - 6
    return d * d + 10 * (1 - t) * math.cos(x[0]) + 10


PROBLEMS = [
    {
        "name": "branin",
        "fn": branin,
        "bounds": [[-5.0, 10.0], [0.0, 15.0]],
        "optimum_value": 0.39788735772973816,
        "n_init": 4,
        "budget": 30,
    },
]

SEEDS = list(range(1, 21))


def run(p):
    runs = []
    for seed in SEEDS:
        r = gp_minimize(
            p["fn"], [tuple(b) for b in p["bounds"]], n_calls=p["budget"],
            n_initial_points=p["n_init"], initial_point_generator="lhs",
            acq_func="EI", xi=0.0, acq_optimizer="lbfgs", n_points=1000,
            n_restarts_optimizer=5, random_state=seed)
        runs.append({"seed": seed, "nfev": len(r.func_vals), "x": [float(v) for v in r.x], "fun": float(r.fun)})
    out = {k: v for k, v in p.items() if k != "fn"}
    out["runs"] = runs
    return out


def main(path):
    doc = {
        "generator": "scripts/reference/scipy/gen_sweep_bo.py",
        "versions": {"numpy": np.__version__, "scipy": scipy.__version__,
                     "scikit-learn": sklearn.__version__, "scikit-optimize": skopt.__version__},
        "problems": [run(p) for p in PROBLEMS],
    }
    body = json.dumps(doc, indent=1) + "\n"
    digest = hashlib.sha256(body.encode()).hexdigest()
    with open(path, "w") as f:
        f.write(body + "\n// golden-hash: " + digest + "\n")


if __name__ == "__main__":
    main(sys.argv[1])
