# Write near_data.csv: 64 rows over five items whose biased covariance
# is EXACTLY L·Lᵀ for a dyadic lower-triangular L with l55 = 2^-24 — a
# near-exact dependency (x5 ≈ a combination of x1..x4) that the one
# singular rule calls singular yet Cholesky factors, every Cholesky step
# exact in any order. Rows walk running means m_k (row k is
# k·m_k − (k−1)·m_{k−1}), so a streaming Welford mean moves by exact
# dyadic steps and its co-moment is exact too: Σ_k k(k−1)·Δm_k·Δm_kᵀ,
# each direction l_j spread over steps whose k(k−1) sum to a power of 4.
#   gen_near_data.py > near_data.csv
import sys
from fractions import Fraction as F
n, p = 64, 5
L = [[2, 0, 0, 0, 0],
     [2, 1, 0, 0, 0],
     [2, 1, 1, 0, 0],
     [2, 0, 1, 1, 0],
     [2, 1, 0, 1, F(1, 2**24)]]
steps = [((2, 3, 8), 64), ((4, 5, 32), 1024), ((14, 20, 22), 1024), ((7, 17, 62), 4096), ((26, 35, 48), 4096)]
dm = {}
for j, (ks, total) in enumerate(steps):
    q = {64: 8, 1024: 32, 4096: 64}[total]
    for k in ks:
        dm[k] = [F(8, q) * L[i][j] for i in range(p)]
m = [F(4)] * p
rows = [m]
for k in range(2, n + 1):
    d = dm.get(k, [F(0)] * p)
    new = [a + b for a, b in zip(m, d)]
    rows.append([k * a - (k - 1) * b for a, b in zip(new, m)])
    m = new
mean = [sum(r[i] for r in rows) / n for i in range(p)]
s = [[sum((r[i] - mean[i]) * (r[j] - mean[j]) for r in rows) / n for j in range(p)] for i in range(p)]
want = [[sum(F(L[i][k]) * L[j][k] for k in range(p)) for j in range(p)] for i in range(p)]
assert s == want, "the rows' covariance is not L·Lᵀ"
for r in rows:
    for v in r:
        assert float(v) == v, "a value is not a double"
print(",".join("x%d" % (i + 1) for i in range(p)))
for r in rows:
    print(",".join(repr(float(v)) for v in r))
