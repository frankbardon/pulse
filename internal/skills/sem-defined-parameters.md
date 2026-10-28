---
name: sem-defined-parameters
description: Defined parameters on MAT_SEM — lavaan's name := expr for indirect, total and contribution effects (a path times a latent mean change) with a delta-method or bootstrap interval and a p. The grammar and its refusals, the convention switch, how the rows read, correcting them together, and telling whether an effect differs from zero.
kind: design
type: guide
applies_to: process, compose, predict
covers: [SEM, defined parameter, indirect effect, mediation, total effect, contribution, delta method, latent mean change, effect decomposition]
requires: [MAT_SEM, capability:sem_defined]
---

# SEM defined parameters

A model line `name := expr` is lavaan's defined parameter: a function of labelled parameters, reported as one more `auxiliary.estimates` row with an estimate, SE, z, p and interval. Use it for an indirect effect (`ind := a*b`), a total, a ratio, or a driver's contribution to a change between groups.

## Grammar

- Operands: labels (`a*x` labels a loading, path, covariance or intercept; every level's label of a per-group `c(a1, a2)*x`), numeric constants (`0.5`, `1.5e-1`) and the names of EARLIER `:=` lines.
- `+ - * / ^` with R's precedence (`-a^2` is `-(a^2)`), parentheses, `sqrt`, `exp`, `log`, `abs`.
- Refused before any row is read (`SERVICE_VALIDATION` `defined_invalid`, `details.problem`): `unknown_label`, `forward_reference` (a later name, itself, a cycle), `duplicate`, `syntax`. lavaan's `==`, `<`, `>` lines are `constraint_unsupported`: write an equality as a shared label.

A `:=` line switches a covariance-only model to lavaan's conventions (Fisher scoring, lavaan's fit indices, `ci_lower` / `ci_upper` columns): `sem-modeling`.

## Reading the rows

- Rows `name := expr` close the table. Under `group` they come after every level, unprefixed: `vectors.group` is `null` and `vectors.label` is the name.
- `std_err` is the delta method: √(gᵀVg), g the expression's exact gradient (forward-mode, never finite differences), V the inverse expected information. `z_value`, the normal `p_value` and the Wald `ci_lower` / `ci_upper`<!-- feature: capability:sem_bootstrap --> (at `ci_level`, default 0.95)<!-- /feature --> follow.
- `std_estimate` is the expression over the standardized solution (lavaan's `std.all`).
- A value that is not finite at the estimate (a log of a negative, a zero divisor) nulls the row and every row built on it, with the warning `PULSE_MATRIX_DEFINED_NONFINITE`.<!-- feature: capability:sem_groups --> A per-group label of a level without rows after the filters is absent from the fit: a `:=` reading it is null with `details.reason` `empty_group`, `label` and `level`.<!-- /feature -->
- `abs()`: Pulse differentiates it by its sign (exact; 0 at 0). lavaan's complex-step jacobian reads that slope as 0, so the estimate matches lavaan and the SE does not.
<!-- feature: capability:sem_bootstrap -->
- Under `resample: "bootstrap"` the `:=` rows take the bootstrap's SE, its normal p and the percentile interval like every free row: `sem-bootstrap`. Prefer it for a product or a ratio, whose sampling distribution is skewed.
<!-- /feature -->

## Recipe: is a driver's contribution to a change non-zero?

Two waves under scalar invariance (`group: "wave"`, `group_equal: ["loadings", "intercepts"]`). Fix each marker intercept (`v1 ~ 0*1`, `s1 ~ 0*1`) so both waves' latent means are free, and label them per wave (`value ~ c(mv1, mv2)*1`). Label the paths into the outcome (`recommend ~ bv*value + c(bs1, bs2)*service`). Then:

```
dv := bv*(mv2 - mv1)
ds := bs2*ms2 - bs1*ms1
dtot := dv + ds
share := dv/dtot
```

`dv` is value's contribution (path × latent mean change), `ds` service's when the path moves too. A contribution is distinguishable from zero when its `p_value` is below alpha AND its interval excludes 0; read the sign from the estimate. Many contributions at once: correct them (below). Runnable: `pulse_examples_search tags=[matrix, comparison]`.

<!-- feature: capability:multiplicity -->
## Multiplicity

A `MAT_SEM` spec joins a correction through its OWN `multiplicity` block only, never an inherited one. The family is every estimates row with a p: the free parameters AND the `:=` rows. `auxiliary.p_adjusted` / `significant_adjusted` are one column each, a row per estimates row (`null` where there is no p); the raw `p_value` never moves. `multiplicity-correction`.
<!-- /feature -->

## See

- `op-mat-sem`
- `sem-modeling` — conventions, means, multi-group fits and invariance.
- `sem-bootstrap` — bootstrap standard errors and percentile intervals.
