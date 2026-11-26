```yaml
name: regression-row-scores
description: Per-row regression columns — the two-pass refit the fitted, residual, leverage and influence attributes run (studentized residuals, Cook's distance, DFFITS), scoring a bound fit with params.fit instead (its leverage and influence from the fit's vcov and dispersion), the scoring rule and its refusals, why the fit's predictor list is authoritative, a registered model's scorer, an ordinal fit's per-level columns, and the save-then-load recipe. Topical design; per-attribute params in the atomic op-attr-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [ATTR_REG, fitted value, residual, leverage, influence, Cook's distance, DFFITS, studentized residual, GLM leverage, dispersion, scoring, params.fit, bound fit, save, load, no_scorer, slot_fields, PULSE_FIT_UNAVAILABLE, ordinal, level, outcome]
```

# Per-row regression scores

Model coefficients come from a regression slot ([`regression-modeling`](regression-modeling.md)); these attributes put a per-row figure from a model into a column. Fits that several slots share: [`compose-fits`](compose-fits.md).

## Refit in the attribute prepass

- `ATTR_REG_FITTED` — the prediction ŷᵢ.
- `ATTR_REG_RESIDUAL` — yᵢ − ŷᵢ.
- `ATTR_REG_LEVERAGE` — the hat-matrix diagonal.
- `ATTR_REG_INFLUENCE` — Cook's distance (`params.measure` `cooks`) or DFFITS (`dffits`).

- `target` / `predictors` / `penalty` / `alpha` / `l1_ratio` are top-level keys, not `params`; `field` is unread. `decimal128` reads as its float64 echo, `packed_bool` as 0/1.
- Two-pass: each slot runs its own OLS fit in the prepass (no sharing).
- A weight follows OLS's kinds (a WLS refit); the residual stays the raw y − ŷ.
- Penalized residuals are bias-shrunk: diagnose unpenalized.

**Studentized residuals.** On the refit `params.kind` `pearson` / `deviance` / `working` equal y − ŷ (OLS is Gaussian, identity). `standardized` (R `rstandard`) is e / (s·√(1 − h)) and `studentized` (R `rstudent`) r·√((df − 1)/(df − r²)), with h the leverage, df = n − p − 1 and s² = RSS/df.

**Design-reading figures** (leverage, influence, the studentized kinds) read the hat and σ:

- Unpenalized only (`PROCESSING_CONFIG`); leverage takes any penalty ([`regression-influence`](regression-influence.md)).
- A frequency weight is w identical observations: influence and studentized figures are ONE copy's (h/w, df = Σw − p − 1). Leverage stays the record's summed h.
- A probability weight refuses the per-observation figures (`PULSE_WEIGHT_UNSUPPORTED`); leverage takes either kind.
- Null outside the fit and where undefined (df ≤ 1, h ≥ 1).

## Scoring a bound fit

`params.fit` names a bound fit (a compose `fits` entry or a loaded `models` entry) instead of refitting — always on the predict attribute (`ATTR_REG_PREDICT`), optionally on the other four:

- Row-local: one pass; it fits nothing. The score equals the bound model's bit for bit.
- The row weight is ignored (the fit's own applied when it ran).
- Fitted reads the fit's score (a GLM: its fitted mean); residual reads y − score, y the fit's target.
- Refusals, predict identically: an unknown name `PULSE_MODEL_UNKNOWN`; a refit field beside it `slot_fields`; a registered model without a scorer `no_scorer`.
- A fit that failed on its data: every row null + `PULSE_FIT_UNAVAILABLE` `{fit, attribute, code}`; strict mode fails the batch with the fit's code.

**Residual scale.** `ATTR_REG_RESIDUAL` over a bound fit takes `params.kind`, as R's `residuals.glm`:

- `response` (the default): y − μ.
- `pearson`: (y − μ)/√V(μ).
- `deviance`: sign(y − μ)·√d(y, μ).
- `working`: (y − μ)·g′(μ).

Unweighted forms (the slot carries no prior weight); on a Gaussian fit every kind equals y − μ. A registered or media-mix fit takes `response` only (a least-squares media-mix fit also the two studentized kinds: [`regression-influence`](regression-influence.md)).

**The design over a bound fit** — leverage, influence and the two studentized kinds read the fit's vcov and dispersion, not its rows: formulas, the admitted fits and their refusals in [`regression-influence`](regression-influence.md).

## The fit's predictor list is authoritative

Build the same predictors in the scoring slot. An absent one-hot dummy is the reference level. Under selection a dropped predictor scores 0. A null predictor nulls the row.

On the predict attribute, `terms` scores a subset (`(intercept)` opt-in) and `bound` reads a coefficient's `lower` / `upper` for one term: a coefficient bound is NOT a prediction interval, and a term with no bound scores null.

## A registered model

An embedder-registered regression scores through its registration's scorer, whole model only (no `terms`, no `lower` / `upper` bound); with none it is `no_scorer`. Its saved model loads only where that regression is registered and enabled, else `PULSE_MODEL_REGRESSION_UNREGISTERED`.

## An ordinal fit

`REG_ORDINAL` predicts a probability per level; one slot is one column, `params.output`:

- `expected` (default): Σ k·P(y = k), levels numbered 1..K in fitted order; `class`: the most probable level's number.
- `probability` (the default with `level`) P(y = level) and `cumulative` P(y ≤ level) need `level`, as written (`"high"`, `"3"`); `for_each` over the levels gives a column each.
- `scale: link` is xᵀβ (no `output` / `level`).
- Refused, predict too: `level` / `output` on another fit (`outcome_unsupported`); an unknown level (`outcome_unknown`, judged by predict when `params.levels` pins them or a model file names them); a missing `level` (`outcome_required`).

## A multinomial fit

`REG_MULTINOMIAL` scores one category per slot: `category` (as in `Categories`) gives P(y = category), the default; `output: class` the most probable category's 0-based index in `Categories`; `scale: link` the category's log-odds vs the reference. `for_each` over the categories gives a column each. A missing `category` is `outcome_required`, an unknown one `outcome_unknown` (predict judges it from a model file; over `fits` the run does). No `terms` / `bound`; the fitted / residual / design attributes refuse it (`type_unsupported`).

## Holdout on a categorical fit

A `holdout` scores each held-out row's probability vector p against its outcome y; slot-weighted means: `log_loss` −ln p_y (natural), `brier` Σ_k (p_k − [y = k])², `accuracy` [argmax p = y] (first on a tie); ordinal adds `rps` Σ_{k<K} (F_k − [y ≤ k])²/(K − 1), F cumulative. No `rmse` / `mae` / `r2`; an outcome the fit lacks is skipped.

## Save, then load

`save: "models/m.pulse-model.json"` on the fit; a later request declares `models: [{name: "m", path: "models/m.pulse-model.json"}]` and scores `fit: "m"` — bit-equal to the in-compose fit, on any cohort with the same predictor fields.

## See

- [`regression-influence`](regression-influence.md), [`regression-modeling`](regression-modeling.md), [`compose-fits`](compose-fits.md), [`regression-spec`](regression-spec.md), [`attribute-composition`](attribute-composition.md)
