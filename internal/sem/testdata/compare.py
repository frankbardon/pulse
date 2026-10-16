# Compare a semopy fit (fit_semopy.py JSON) with a Pulse MAT_SEM result:
#   compare.py semopy.json pulse.json [matrix-index]
# pulse.json is `pulse api process --json` output (its data.matrices[i]).
import json, sys, math
ref, got = json.load(open(sys.argv[1])), json.load(open(sys.argv[2]))
res = got['data']['matrices'][int(sys.argv[3]) if len(sys.argv) > 3 else 0]; est = res['auxiliary']['estimates']
g = {k: dict(zip(est['column_keys'], v)) for k, v in zip(est['row_keys'], est['values'])}
rel = lambda a, b: abs(a - b) / max(1.0, abs(b))
worst = {c: (0.0, '') for c in ['estimate', 'std_estimate', 'std_err', 'z_value', 'p_value']}
cols = {'estimate': 'est', 'std_estimate': 'std', 'std_err': 'se', 'z_value': 'z', 'p_value': 'p'}
missing = []
for p in ref['params']:
    k = f"{p['lval']} {p['op']} {p['rval']}"
    if k not in g and p['op'] == '~~':
        k = f"{p['rval']} ~~ {p['lval']}"
    if k not in g: missing.append(k); continue
    for c, rc in cols.items():
        if p[rc] is None: continue
        d = abs(g[k][c] - p[rc]) if c == 'p_value' else rel(g[k][c], p[rc])
        if d > worst[c][0]: worst[c] = (d, k)
statmap = {'DoF': 'dof', 'DoF Baseline': 'dof_baseline', 'chi2': 'chi2', 'chi2 p-value': 'chi2_p_value', 'chi2 Baseline': 'chi2_baseline',
           'CFI': 'cfi', 'GFI': 'gfi', 'AGFI': 'agfi', 'NFI': 'nfi', 'TLI': 'tli', 'RMSEA': 'rmsea', 'AIC': 'aic', 'BIC': 'bic', 'LogLik': 'loglik'}
print(f"  rows: semopy {len(ref['params'])}, pulse {len(g)}, missing {missing}")
print(f"  F: semopy {ref['fun']:.12f}  pulse {res['scalars']['objective']:.12f}   semopy time {ref['seconds']:.2f}s")
for c, (d, k) in worst.items(): print(f"  max {'abs' if c == 'p_value' else 'rel'} diff {c:13s} {d:.2e}  ({k})")
sw = max(((rel(res['scalars'][v], ref['stats'][s]) if not (math.isnan(ref['stats'][s])) else 0.0, s) for s, v in statmap.items()))
print(f"  max rel diff fit stat      {sw[0]:.2e}  ({sw[1]})")
