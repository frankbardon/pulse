# Simulate a survey-style dataset from a model file's structure:
#   simulate.py N SEED model.txt > data.csv   (items rounded to a 1–7 scale)
# Every `=~` loading is drawn in [0.5, 1.3] (the first fixed at 1), the
# top latent is N(0, 1), and each `lhs ~ latent` outcome is a linear
# function of its latent plus noise.
import sys, numpy as np, pandas as pd
n = int(sys.argv[1]) if len(sys.argv) > 1 else 1200
rng = np.random.default_rng(int(sys.argv[2]) if len(sys.argv) > 2 else 7)
meas, regs = {}, []
for line in open(sys.argv[3] if len(sys.argv) > 3 else 'model.txt'):
    line = line.split('#')[0].strip()
    if '=~' in line:
        l, r = line.split('=~'); meas[l.strip()] = [x.strip() for x in r.split('+')]
    elif '~' in line and '~~' not in line:
        l, r = line.split('~'); regs.append(([x.strip() for x in l.split(',')], r.strip()))
vals = {}
def gen(v):
    if v in vals: return vals[v]
    parent = next((l for l, inds in meas.items() if v in inds), None)
    if parent is None:
        vals[v] = rng.normal(0, 1, n); return vals[v]
    lam = 1.0 if meas[parent][0] == v else rng.uniform(0.5, 1.3)
    noise = rng.uniform(0.3, 0.8) if v in meas else rng.uniform(0.4, 1.2)
    vals[v] = lam * gen(parent) + rng.normal(0, np.sqrt(noise), n)
    return vals[v]
for l, inds in meas.items():
    for i in inds: gen(i)
for outs, latent in regs:
    for o in outs:
        vals[o] = rng.uniform(0.4, 0.8) * gen(latent) + rng.normal(0, 0.7, n)
obs = [k for k in vals if k not in meas]
df = pd.DataFrame({k: vals[k] for k in obs})
df = (df * 1.1 + 4).round().clip(1, 7)
df.to_csv(sys.stdout, index=False)
