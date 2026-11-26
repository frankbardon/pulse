---
name: regression-panel-covariance
description: Panel-robust standard errors on a regression over entities × periods (geo × week, store × day) — per-entity Newey–West (covariance hac + entity) and Driscoll–Kraay (covariance driscoll_kraay), two-way clustering (cluster + cluster2), how periods and lags are counted on an unbalanced panel, the entity rules, the echo and the refusals, against R sandwich::vcovPL and plm. Topical design; the other estimators live in regression-inference.
type: guide
kind: design
applies_to: process, compose, predict
covers: [panel, entity, driscoll_kraay, cluster2, two-way cluster, vcovCL, Driscoll-Kraay, per-entity Newey-West, vcovPL, vcovSCC, vcovNW, hac, order_by, lags, covariance, regressions]
---

# Panel-robust covariance

A panel is many entities (geos, stores, users) observed over the same periods. A plain `hac` reads ONE series — a week shared by two geos is a tie and is refused (`order_by_ties`). Two panel estimators read the panel instead; both swap the matrix behind `std_errors`, `p_values`, the intervals and `vcov` (√diag = `StdErrors`), keep the coefficients, and run buffered.

| Want | `covariance` |
|---|---|
| Serial correlation within each entity, entities independent | `{"type": "hac", "order_by": "week", "entity": "geo"}` |
| Serial AND cross-sectional correlation (a shared shock hits every geo in a week) | `{"type": "driscoll_kraay", "order_by": "week"}` (`entity` optional) |
| Rows correlated within a geo AND within a week, no lag structure | `{"type": "cluster", "cluster": "geo", "cluster2": "week"}` |

<!-- feature: REG_OLS -->
```json cohort=mmm_weekly
{"cohort": {"filename": "mmm_weekly.pulse"},
 "regressions": [{"type": "REG_OLS", "target": "sales", "predictors": ["tv", "price"],
  "covariance": {"type": "driscoll_kraay", "order_by": "week", "entity": "market", "lags": 3}}]}
```
<!-- /feature -->

## How it is computed

Each row's score is u = w*·g (the unit-weight score g of the fit, w* the inference weight). The periods are the distinct `order_by` values over the fitted rows, ranked 1..T.

- **Per-entity Newey–West** (`hac` + `entity`): every entity is its own series; the Bartlett meat pairs an entity's rows `l` periods apart with weight 1 − l/(L+1), l ≤ L, and never pairs two entities.
- **Driscoll–Kraay** (`driscoll_kraay`): the scores of one period are summed across entities, S_t = Σ u, then the Newey–West meat runs over S_1..S_T.

Both scale by N*/(N* − k). A lag counts PERIODS, not rows: on an unbalanced panel an entity missing week 5 pairs its weeks 4 and 6 at lag 2, and contributes no lag-1 pair there.

`lags`: a non-negative integer, or `"auto"` (default) = ⌊4(T/100)^(2/9)⌋ over the T periods — not over the rows, as a plain `hac` counts.

## Fields

- `order_by` (required): date, datetime or numeric.
- `entity`: categorical, unsigned integer, date, datetime or packed_bool — the `order.partition` key types; a label or a number keys it. Required for the per-entity `hac`; optional for `driscoll_kraay`, where it only asserts the panel shape (period sums do not read it).
- An entity repeating an `order_by` value is refused at run time (`PROCESSING_REGRESSION_VCOV_UNSUPPORTED`, `details.reason` `order_by_ties`, with `entity` / `entity_value` / `value`); different entities sharing a period is the point. Predict cannot see it.
- A null `entity` or `order_by` drops the row.

## Inference and echo

<!-- feature: REG_OLS -->
REG_OLS reads it on t with the residual df N* − k.
<!-- /feature -->
<!-- feature: REG_GLM -->
REG_GLM stays Wald z (the IRLS-score sandwich, dispersion-free).
<!-- /feature -->
<!-- feature: REG_BAYES_LINEAR -->
REG_BAYES_LINEAR: Müller's sandwich posterior with this meat; normal intervals, no p-values.
<!-- /feature -->

`covariance` echoes `{type, order_by, lags, periods}` plus `entity` and `entities` (its level count) when an entity is set — `lags` resolved. A plain `hac` echo has no `periods`.

## Refusals

Shape faults are `PROCESSING_CONFIG` in predict and at run time: `driscoll_kraay` without `order_by`; `entity` / `order_by` / `lags` on another type; `entity` naming the `order_by` field. Beside a penalty, resample, selection or OLS `bounds`, or on a type without the estimators, it is `PROCESSING_REGRESSION_VCOV_UNSUPPORTED` (as every robust type).

## Two-way cluster

`cluster2` beside `cluster` (same field types; OLS, GLM and Bayes-linear fits): Cameron–Gelbach–Miller V₁ + V₂ − V₁∩₂, each one-way meat at its own G/(G − 1), the intersection cells the third, all × (N* − 1)/(N* − k). OLS reads t on min(G₁, G₂) − 1 df. The echo adds `cluster2` / `clusters2`. The meat is a difference: with few clusters a variance can come out NEGATIVE — kept in `vcov`, its `std_errors`, p-value and interval `null` (never 0). Few clusters on either field: prefer `driscoll_kraay`. Refused: `cluster2` off type `cluster` or naming `cluster` (`PROCESSING_CONFIG`); on the multinomial or an extension model (reason `type`); a field with < 2 levels (reason `clusters`, run time). R: `sandwich::vcovCL(cluster = ~a + b, type = "HC1")`.

## Against R

`hac` + `entity` = `sandwich::vcovPL(fit, cluster = ~entity, order.by = ~period, aggregate = FALSE, lag = L, adjust = TRUE)` = `plm::vcovNW(pooling, maxlag = L, type = "HC1")`; `driscoll_kraay` = `vcovPL(..., aggregate = TRUE)` = `plm::vcovSCC(..., type = "HC1")`. `"auto"` is vcovPL's `lag = "NW1994"` (plm defaults to ⌊T^(1/4)⌋ — pass `lags` to match it). Weighted: vcovPL on the w*-weighted fit with `adjust = FALSE`, times N*/(N* − k). Oracle `robust_vcov_panel.json`.

## See

- `regression-inference` — the other estimators, intervals and `vcov`.
<!-- feature: REG_OLS -->
- `op-reg-ols` — the `covariance` Param.
<!-- /feature -->
<!-- feature: REG_GLM -->
- `op-reg-glm` — the `covariance` Param.
<!-- /feature -->
<!-- feature: REG_BAYES_LINEAR -->
- `op-reg-bayes-linear` — the `covariance` Param.
<!-- /feature -->
