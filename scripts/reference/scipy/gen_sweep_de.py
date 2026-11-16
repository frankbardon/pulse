# /// script
# requires-python = "==3.12.*"
# dependencies = [
#   "numpy==2.3.3",
#   "scipy==1.16.2",
# ]
# ///
"""Differential-evolution oracle for the compose sweep's mode de
(compose-sweep-2 E3-S3, internal/sweep/de.go).

scipy.optimize.differential_evolution with the settings mode de runs:
strategy rand1bin, a constant differential weight F (mutation=F, no
dither), crossover probability CR (recombination=CR), a Latin-hypercube
initial population of NP = popsize * D members, synchronous generations
(updating="deferred"), no polishing and no early stop (tol = atol = 0),
so the run spends exactly NP * (maxiter + 1) evaluations — the sweep
budget.

The two engines draw from different random streams (and scipy resamples
an out-of-bounds trial coordinate where mode de clips it), so their
trajectories cannot match; the oracle pins the OUTCOME instead: per
seed, the best point and value scipy reaches on the same problem and
budget. The Go test (internal/sweep/de_oracle_heavy_test.go) runs mode
de over as many seeds and compares the distributions.

Run: make reference-sweep-de (uv run, pinned deps). Writes
internal/sweep/testdata/de_oracle.json with a // golden-hash: footer;
never hand-edit it.
"""
import hashlib
import json
import sys

import numpy as np
import scipy
from scipy.optimize import differential_evolution


def rosenbrock(x):
    return (1.0 - x[0]) ** 2 + 100.0 * (x[1] - x[0] ** 2) ** 2


PROBLEMS = [
    {
        "name": "rosenbrock2",
        "fn": rosenbrock,
        "bounds": [[-2.0, 2.0], [-2.0, 2.0]],
        "optimum": [1.0, 1.0],
        "population": 20,
        "budget": 2000,
        "f": 0.8,
        "cr": 0.9,
    },
]

SEEDS = list(range(1, 21))


def run(p):
    d = len(p["bounds"])
    gens = p["budget"] // p["population"]
    runs = []
    for seed in SEEDS:
        r = differential_evolution(
            p["fn"], p["bounds"], strategy="rand1bin", maxiter=gens - 1,
            popsize=p["population"] // d, mutation=p["f"], recombination=p["cr"],
            init="latinhypercube", updating="deferred", polish=False,
            tol=0.0, atol=0.0, rng=seed)
        runs.append({"seed": seed, "nfev": int(r.nfev), "x": [float(v) for v in r.x], "fun": float(r.fun)})
    out = {k: v for k, v in p.items() if k != "fn"}
    out["runs"] = runs
    return out


def main(path):
    doc = {
        "generator": "scripts/reference/scipy/gen_sweep_de.py",
        "versions": {"numpy": np.__version__, "scipy": scipy.__version__},
        "problems": [run(p) for p in PROBLEMS],
    }
    body = json.dumps(doc, indent=1) + "\n"
    digest = hashlib.sha256(body.encode()).hexdigest()
    with open(path, "w") as f:
        f.write(body + "\n// golden-hash: " + digest + "\n")


if __name__ == "__main__":
    main(sys.argv[1])
