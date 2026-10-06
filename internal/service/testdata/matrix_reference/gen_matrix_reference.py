# /// script
# requires-python = "==3.12.*"
# dependencies = [
#   "numpy==2.3.3",
#   "statsmodels==0.14.5",
# ]
# ///
"""Reference generator for MAT_COVARIANCE (U16 E1-S2),
MAT_CORRELATION (U16 E3-S1) and their pairwise mode (U16 E3-S2).

Writes ../../matrix_reference_values_test.go (package service): the
fixture rows; per (weight column, ddof), the covariance matrix and its
determinant; and per weight column, the Pearson correlation matrix and
its determinant the external references compute. CI never runs Python;
the Go literals ARE the pinned oracle. Never hand-edit the output.

Regenerate:

    cd internal/service/testdata/matrix_reference
    uv run --no-project gen_matrix_reference.py
    gofmt -w ../../matrix_reference_values_test.go

References, and how each maps onto Pulse's definition (listwise: a row
with any member null is dropped; Cov = M2_w / (Σw − ddof)):

  * unweighted — numpy.cov(rows, rowvar=False, ddof=ddof) on the
    listwise-complete rows.
  * frequency weights f — statsmodels DescrStatsW(rows, weights=f,
    ddof=ddof).cov, cross-checked against numpy.cov(fweights=f).
  * probability weights p — DescrStatsW(rows, weights=p, ddof=ddof).cov
    (statsmodels divides by sum_weights − ddof, Pulse's denominator).
  * determinant — numpy.linalg.det of the reference covariance.
  * correlation — numpy.corrcoef(rows, rowvar=False) unweighted;
    DescrStatsW(rows, weights=w).corrcoef weighted (r is scale-free, so
    ddof and the weight kind cancel), cross-checked against the
    covariance normalised by its diagonal. Determinant: numpy.linalg.det.

Pairwise (params.missing "pairwise"): every cell (i, j) applies the
same reference to the rows where BOTH x_i and x_j are present (the
diagonal: the rows where x_i is present) — numpy.cov / DescrStatsW.cov
on that two-column subset for the covariance, numpy.corrcoef /
DescrStatsW.corrcoef on it for r (each pair's own variances, as
pandas.DataFrame.corr does). The pairwise N is that row count. The
determinant is numpy.linalg.det when numpy.linalg.cholesky succeeds,
NaN (null) otherwise.

A row of weight 0 is kept (it counts toward n and adds no mass, which
neither reference distinguishes from dropping it).
"""

import pathlib

import numpy as np
from statsmodels.stats.weightstats import DescrStatsW

NULL = None
# x1, x2, x3, f (frequency), p (probability)
ROWS = [
    (2.5, 10.0, 1.0, 1, 0.50),
    (3.1, 12.5, 0.0, 2, 1.25),
    (NULL, 9.0, 1.0, 1, 0.75),
    (4.7, 15.25, 1.0, 3, 2.00),
    (1.2, 7.5, 0.0, 1, 0.30),
    (5.9, NULL, 1.0, 2, 1.10),
    (3.3, 11.0, 0.0, 0, 0.00),
    (6.4, 19.75, 1.0, 1, 0.90),
    (2.0, 8.25, 0.0, 4, 1.60),
    (4.1, 13.5, NULL, 1, 0.45),
    (3.8, 12.0, 1.0, 2, 1.35),
    (0.9, 6.0, 0.0, 1, 0.20),
]

complete = [r for r in ROWS if None not in r[:3]]
X = np.array([r[:3] for r in complete], dtype=float)
F = np.array([r[3] for r in complete], dtype=float)
P = np.array([r[4] for r in complete], dtype=float)


def cov(weight, ddof):
    if weight is None:
        return np.cov(X, rowvar=False, ddof=ddof)
    w = F if weight == "f" else P
    c = DescrStatsW(X, weights=w, ddof=ddof).cov
    if weight == "f":
        ref = np.cov(X, rowvar=False, ddof=ddof, fweights=F.astype(int))
        assert np.allclose(c, ref, rtol=1e-13, atol=0), (c, ref)
    return c


def corr(weight):
    if weight is None:
        c = np.corrcoef(X, rowvar=False)
    else:
        w = F if weight == "f" else P
        c = DescrStatsW(X, weights=w).corrcoef
    cv = cov(weight, 0)
    sd = np.sqrt(np.diag(cv))
    ref = cv / np.outer(sd, sd)
    assert np.allclose(c, ref, rtol=1e-13, atol=0), (c, ref)
    return c


def pair_rows(i, j):
    keep = [r for r in ROWS if r[i] is not None and r[j] is not None]
    x = np.array([[r[i], r[j]] for r in keep], dtype=float)
    f = np.array([r[3] for r in keep], dtype=float)
    p = np.array([r[4] for r in keep], dtype=float)
    return x, f, p


def pair_weights(weight, f, p):
    return None if weight is None else (f if weight == "f" else p)


def pairwise_cov(weight, ddof):
    c = np.zeros((3, 3))
    for i in range(3):
        for j in range(3):
            x, f, p = pair_rows(i, j)
            w = pair_weights(weight, f, p)
            if w is None:
                c[i, j] = np.cov(x, rowvar=False, ddof=ddof)[0, 1]
            else:
                c[i, j] = DescrStatsW(x, weights=w, ddof=ddof).cov[0, 1]
    return c


def pairwise_corr(weight):
    c = np.eye(3)
    for i in range(3):
        for j in range(3):
            if i == j:
                continue
            x, f, p = pair_rows(i, j)
            w = pair_weights(weight, f, p)
            if w is None:
                c[i, j] = np.corrcoef(x, rowvar=False)[0, 1]
            else:
                c[i, j] = DescrStatsW(x, weights=w).corrcoef[0, 1]
    return c


def pairwise_n():
    return [[len(pair_rows(i, j)[0]) for j in range(3)] for i in range(3)]


def det_or_nan(c):
    try:
        np.linalg.cholesky(c)
    except np.linalg.LinAlgError:
        return None
    return np.linalg.det(c)


def lit(v):
    if v is None:
        return "math.NaN()"
    return repr(float(v))


out = []
out.append("// Code generated by testdata/matrix_reference/gen_matrix_reference.py; DO NOT EDIT.\n")
out.append("// numpy " + np.__version__ + ", statsmodels DescrStatsW.\n\n")
out.append("package service\n\nimport \"math\"\n\n")
out.append("// matrixRefRows are x1, x2, x3, f (frequency weight), p (probability\n// weight); NaN is a null cell.\n")
out.append("var matrixRefRows = [][5]float64{\n")
for r in ROWS:
    out.append("\t{" + ", ".join(lit(v) for v in r) + "},\n")
out.append("}\n\n")
out.append("// matrixRefCases: weight column (\"\" = unweighted), ddof, covariance, determinant.\n")
out.append("var matrixRefCases = []struct {\n\tweight string\n\tddof   int\n\tcov    [3][3]float64\n\tdet    float64\n}{\n")
for weight in (None, "f", "p"):
    for ddof in (0, 1):
        c = cov(weight, ddof)
        det = np.linalg.det(c)
        rows = ", ".join("{" + ", ".join(repr(float(v)) for v in row) + "}" for row in c)
        out.append("\t{%s, %d, [3][3]float64{%s}, %r},\n" % ('"' + (weight or "") + '"', ddof, rows, float(det)))
out.append("}\n\n")
out.append("// matrixRefCorrCases: weight column (\"\" = unweighted), Pearson correlation, determinant.\n")
out.append("var matrixRefCorrCases = []struct {\n\tweight string\n\tcorr   [3][3]float64\n\tdet    float64\n}{\n")
for weight in (None, "f", "p"):
    c = corr(weight)
    det = np.linalg.det(c)
    rows = ", ".join("{" + ", ".join(repr(float(v)) for v in row) + "}" for row in c)
    out.append("\t{%s, [3][3]float64{%s}, %r},\n" % ('"' + (weight or "") + '"', rows, float(det)))
out.append("}\n")

out.append("\n// matrixRefPairwiseN: the pairwise N (rows where both members are present).\n")
out.append("var matrixRefPairwiseN = [3][3]int64{" + ", ".join("{" + ", ".join(str(v) for v in row) + "}" for row in pairwise_n()) + "}\n\n")
out.append("// matrixRefPairwiseCases: operator, weight column (\"\" = unweighted), ddof\n// (covariance only), pairwise matrix, determinant (NaN = not positive definite).\n")
out.append("var matrixRefPairwiseCases = []struct {\n\ttyp    string\n\tweight string\n\tddof   int\n\tm      [3][3]float64\n\tdet    float64\n}{\n")
for weight in (None, "f", "p"):
    for ddof in (0, 1):
        c = pairwise_cov(weight, ddof)
        rows = ", ".join("{" + ", ".join(repr(float(v)) for v in row) + "}" for row in c)
        out.append("\t{%s, %s, %d, [3][3]float64{%s}, %s},\n" % ('"MAT_COVARIANCE"', '"' + (weight or "") + '"', ddof, rows, lit(det_or_nan(c))))
for weight in (None, "f", "p"):
    c = pairwise_corr(weight)
    rows = ", ".join("{" + ", ".join(repr(float(v)) for v in row) + "}" for row in c)
    out.append("\t{%s, %s, 0, [3][3]float64{%s}, %s},\n" % ('"MAT_CORRELATION"', '"' + (weight or "") + '"', rows, lit(det_or_nan(c))))
out.append("}\n")

dest = pathlib.Path(__file__).resolve().parent.parent.parent / "matrix_reference_values_test.go"
dest.write_text("".join(out))
print("wrote", dest)
