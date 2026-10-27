---
name: sem-bootstrap
description: Bootstrap standard errors, p-values and percentile intervals for a structural equation model — lavaan's se = "bootstrap" on MAT_SEM, seeded and reproducible, within-group under a multi-group fit. When to prefer it over the analytic (delta-method) figures, what it costs and how to read the replicate counts.
kind: design
type: guide
applies_to: process, compose, predict
covers: [SEM, bootstrap, resample, percentile interval, bootstrap standard error, indirect effect, rng_seed, ci_level]
requires: [MAT_SEM, capability:sem_bootstrap]
---

# SEM bootstrap

`resample: "bootstrap"` on `MAT_SEM` replaces the analytic standard errors with lavaan's `se = "bootstrap"`. Use it when the analytic figures are doubtful: a skewed estimate such as an indirect effect or a ratio (`name := expr`), a small sample, or non-normal items.

## How it runs

- `bootstrap_iters` replicates (default 1000). Each one draws the listwise rows with replacement and refits the model with the slot's options. Under `group`, rows are drawn WITHIN each level, so every replicate keeps each group's n.
- `rng_seed` seeds the draws. The same rows and seed give the same figures bit for bit, at any worker or shard count. Absent (or 0), the seed is derived from the request and the slot's rows, as a regression's `resample` derives it (`op-reg-mod-resample`; `params` key order and spacing never move it): the same request over the same data draws the same replicates, and a changed request or data draws new ones. Pin a non-zero seed for draws that stay put.
- The slot keeps its rows, so the request runs the buffered path (predict says so). Each replicate is a full refit: 1000 replicates cost about 1000 fits.
- The bootstrap switches the fit to lavaan's conventions (Fisher scoring, lavaan's fit indices, `ci_lower` / `ci_upper` columns).

## Reading the outputs

- `estimate` and `std_estimate` stay the full data's.
- On every free row and every `:=` row, `std_err` is the replicates' standard deviation. `z_value` = estimate / `std_err`, and `p_value` is its two-sided normal p, as lavaan reports it: normal theory over the bootstrap SE, not the share of replicates crossing 0 (a regression's `resample` p).
- `ci_lower` / `ci_upper` are the percentile interval at `ci_level` (default 0.95), lavaan's `boot.ci.type = "perc"`. It need not be symmetric about the estimate: on a skewed effect it is the figure to read, not the p.
- Fixed rows keep `null` figures.
- `scalars.bootstrap_requested` is the replicates run, `bootstrap_succeeded` those that refitted. A replicate whose fit fails, stops short of convergence or gives a non-finite figure is skipped; every figure reads the successes only. A large gap means the model is unstable on resampled data.

## Refusals

- Fewer than 10 successful replicates fail the slot alone: `PULSE_MATRIX_NOT_CONVERGED`, `details.reason` `bootstrap_too_few`.
- Any weight, a frequency weight included, is `PULSE_WEIGHT_UNSUPPORTED`: a weighted bootstrap has no standard form.
- `params.fit` cannot bind a bootstrapped slot (it reads no rows).
- `bootstrap_iters` or `rng_seed` without `resample` is `bad_params`. `ci_level` alone sets the Wald interval of any lavaan-convention fit, and is `bad_params` on a semopy-convention one.
- `bootstrap_iters` has no instance limit, as a regression's has not. `Options.Limits.RequestTimeout` stops a long run between replicates.

## See

- `op-mat-sem`
- `sem-modeling` — conventions, means and multi-group fits.
- `sem-defined-parameters` — `:=` effects, the contribution recipe.
- `multiplicity-correction` — correcting the bootstrap p-values like any other.
