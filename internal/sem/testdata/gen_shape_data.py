# Write shape_data.csv: 600 synthetic rows for shape63_model.txt and
# shape89_model.txt — 89 indicators x01..x89 (0/1 except the mediator's
# and the outcome factor's 1–5 items x55..x63), four 0–10 outputs y1..y4
# and a row id. Four correlated constructs c1..c4 each drive three
# first-order factors; f3 has NO disturbance (f3 = 0.9·c1 exactly), the
# near-zero latent variance the parity test probes. A few cells of x05
# and y2 are blank (listwise drops). Rows with id < SINGULAR_ROWS keep
# spread on every column, so the singular-subset fit has no
# zero-spread member.
#   gen_shape_data.py > shape_data.csv
import sys
import numpy as np

n = 600
SINGULAR_ROWS = 50
rng = np.random.default_rng(20261022)
c = rng.multivariate_normal(np.zeros(4), 0.7 * np.eye(4) + 0.3, n).T
lam = [1.0, 0.8, 0.9]
f = []
for k in range(12):
    base = c[k // 3]
    if k == 2:
        f.append(0.9 * base)  # no disturbance: f3's latent variance is ~0
    else:
        f.append(lam[k % 3] * base + rng.normal(0, np.sqrt(0.4), n))
m = 0.3 * c[0] + 0.3 * c[1] + 0.2 * c[2] + 0.2 * c[3] + rng.normal(0, 0.6, n)
g = 0.5 * m + 0.3 * c[0] + rng.normal(0, 0.6, n)
blocks = [(1, 6), (7, 12), (13, 18), (19, 24), (25, 30), (31, 36), (37, 42), (43, 48), (49, 54), (64, 72), (73, 81), (82, 89)]
cols = {}
for k, (a, b) in enumerate(blocks):
    for i in range(a, b + 1):
        tau = rng.uniform(-0.5, 0.5)
        cols[i] = (rng.uniform(0.6, 1.2) * f[k] + rng.normal(0, 0.8, n) > tau).astype(float)
for lat, (a, b) in ((m, (55, 58)), (g, (59, 63))):
    for i in range(a, b + 1):
        cols[i] = np.clip(np.round(3 + rng.uniform(0.7, 1.2) * lat + rng.normal(0, 0.7, n)), 1, 5)
ys = [np.clip(np.round(5 + 2 * rng.uniform(0.4, 0.8) * g + rng.normal(0, 1.2, n)), 0, 10) for _ in range(4)]
ids = np.arange(n)
miss05 = ids % 37 == 5
missy2 = ids % 53 == 7
sub = ids < SINGULAR_ROWS
for i, v in cols.items():
    keep = sub & ~miss05 if i == 5 else sub
    assert v[keep].min() < v[keep].max(), "x%02d has no spread in the singular subset" % i
print(",".join(["id"] + ["x%02d" % i for i in range(1, 90)] + ["y%d" % i for i in range(1, 5)]))
for r in range(n):
    row = [str(r)]
    for i in range(1, 90):
        row.append("" if i == 5 and miss05[r] else "%d" % cols[i][r])
    for j, y in enumerate(ys):
        row.append("" if j == 1 and missy2[r] else "%d" % y[r])
    print(",".join(row))
