# gen_diagnostics.py — ArviZ oracles for the public mcmc package's
# convergence diagnostics (bayesian-mmm E1-S1):
#
#   diagnostics.json  mcmc.RHat / mcmc.ESSBulk / mcmc.ESSTail
#                     vs  arviz_stats rhat(method="rank"),
#                         ess(method="bulk"), ess(method="tail", prob=0.05)
#
# Run via `make reference-mcmc` (which appends the `// golden-hash:`
# footer TestMCMC_OracleGoldensHashed checks), or on its own:
#
#   python3 -I mcmc/testdata/gen_diagnostics.py mcmc/testdata
#
# CI never runs Python: the JSON is committed. The fixture chains are
# generated here from a seeded numpy Generator and stored in the JSON
# itself, so the Go test reads exactly the draws ArviZ saw. Every number
# is written with Python's shortest round-trip repr, and a rerun is
# byte-identical. An undefined reference (ArviZ NaN) is written as null.
#
# Pinned packages (the script refuses any other version): arviz-stats
# 1.3.3, numpy 2.5.4, scipy 1.18.1. Install into a throwaway venv:
#
#   python3 -m venv /tmp/mcmc-ref && /tmp/mcmc-ref/bin/pip install \
#       arviz-stats==1.3.3 numpy==2.5.4 scipy==1.18.1
#   PYTHON=/tmp/mcmc-ref/bin/python3 make reference-mcmc

import json
import math
import os
import sys

import arviz_stats
import numpy as np
import scipy
from arviz_stats.base import array_stats

PINS = {"arviz-stats": "1.3.3", "numpy": "2.5.4", "scipy": "1.18.1"}
HAVE = {
    "arviz-stats": arviz_stats.__version__,
    "numpy": np.__version__,
    "scipy": scipy.__version__,
}
for name, want in PINS.items():
    if HAVE[name] != want:
        sys.exit(f"gen_diagnostics.py: {name} {HAVE[name]} installed, pinned {want}")


def ar1(rng, n_chain, n_draw, phi, mean=0.0, scale=1.0):
    out = np.empty((n_chain, n_draw))
    for c in range(n_chain):
        x = rng.normal() / math.sqrt(1.0 - phi * phi)
        for i in range(n_draw):
            x = phi * x + rng.normal()
            out[c, i] = x
    return out * scale + mean


def cases():
    rng = np.random.default_rng(62)
    yield "ar1_4x1000", ar1(rng, 4, 1000, 0.9)
    shifted = ar1(rng, 4, 500, 0.5)
    shifted[3] += 1.5
    yield "mean_shift_4x500", shifted
    scaled = rng.normal(size=(4, 400))
    scaled[2] *= 4.0
    yield "scale_shift_4x400", scaled
    yield "cauchy_odd_3x101", rng.standard_cauchy(size=(3, 101))
    yield "poisson_ties_4x200", rng.poisson(2.0, size=(4, 200)).astype(float)
    yield "antithetic_2x300", ar1(rng, 2, 300, -0.6)
    yield "single_chain_1x250", ar1(rng, 1, 250, 0.5)
    yield "minimal_2x4", rng.normal(size=(2, 4))
    yield "drift_4x300", ar1(rng, 4, 300, 0.99) + np.linspace(0, 3, 300)
    yield "constant_2x10", np.full((2, 10), 3.0)


def num(v):
    v = float(v)
    return None if math.isnan(v) else v


def main(outdir):
    doc = {
        "generator": "mcmc/testdata/gen_diagnostics.py",
        "python_version": sys.version.split()[0],
        "packages": HAVE,
        "cases": [],
    }
    for name, chains in cases():
        chains = np.round(chains, 10)
        doc["cases"].append(
            {
                "name": name,
                "chains": [[float(x) for x in row] for row in chains],
                "rhat": num(array_stats.rhat(chains, method="rank")),
                "ess_bulk": num(array_stats.ess(chains, method="bulk")),
                "ess_tail": num(array_stats.ess(chains, method="tail", prob=0.05)),
            }
        )
    path = os.path.join(outdir, "diagnostics.json")
    with open(path, "w") as f:
        json.dump(doc, f, indent=1)
        f.write("\n")


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else os.path.dirname(os.path.abspath(__file__)))
