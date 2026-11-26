```yaml
name: regression-order
description: Fitting a regression's rows in time order — the shared order block {field, partition, gaps}, how the engine sorts and gap-checks the admitted rows before any fit, the duplicate / gap refusals, panels, the models that require an order, the Bayesian media-mix model's deprecated params.time alias, and adstock carry-over into held-out and scored rows (carryover, adstock_state). Topical design; per-model params in atomic op-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [order, row order, time order, order.field, order.partition, gaps, PROCESSING_REGRESSION_ORDER, order_required, panel, params.time, carryover, adstock carry-over, adstock_state]
requires: [capability:regression_order]
```

# Regression order

A model that carries state from one row to the next — an adstock, a lag, a state-space filter — needs its rows in time order, contiguous. `order` is the one place a regression says so; the engine enforces it before any fit.

## The block

`"order": {"field": "week", "partition": "market", "gaps": "refuse"}` on any regression entry.

| Key | Meaning |
|---|---|
| `field` | (required) The ordering field: an unsigned integer period index, a `date` (days) or a `datetime` (seconds); a cohort field or a derived label. |
| `partition` | Optional: the field splitting a panel into series (a categorical, unsigned integer, `date`, `datetime` or `packed_bool`). Each series is ordered and checked alone; ragged starts and ends are fine. |
| `gaps` | `refuse` (the default and only policy). |

Every built-in and every registered model accepts it. It forces the buffered path.

## What the engine does

1. Drops a row whose `field` or `partition` is null (it cannot be placed).
2. Sorts the rows by `partition` (labels A–Z, numbers ascending), then `field`; equal keys keep record order.
3. Over the rows the fit admits — target and every predictor present and finite — checks each series: a repeated `field` value is `reason: duplicate`; a step that is not the cadence (the smallest positive step over every series) is `reason: gap`. Either fails that slot alone with `PROCESSING_REGRESSION_ORDER` (`details.field`, `partition`, `value` or `from` / `to` / `step`); the rest of the request runs.
4. Hands the model its rows in that order. A `holdout` fits the ordered train rows (gap-checked) and scores the held-out rows in order too.

A filtered or null-target row inside the span leaves a gap — the adstock would carry across it. Fill the period, filter to one complete span, or set `partition` for a panel (a repeat across markets is not a duplicate within one). Months on a `date` read as gaps (28–31 day steps): order them by an integer period index.

## Spec refusals (predict and run alike)

- `PROCESSING_CONFIG` `{slot: "order", reason}`: `field_missing`, `gaps_invalid`, `partition_is_field`, `field_type` (a float, categorical, …), `partition_type`.
- An unknown `field` / `partition` is the field-reference refusal.
- A model that requires an order, without one: `reason: order_required` (`details.requires: order`).

## REG_MMM_BAYES

`REG_MMM_BAYES` reads `order.field` as its time field; `params.time` is the deprecated alias (same fit, same seed — `order: {field: "week"}` draws exactly what `params: {time: "week"}` drew). Both set must name the same field (`reason: time_alias_conflict`). On a geo panel `partition` must be `params.geo`, and without `params.geo` a `partition` is refused (`reason: order_partition_not_geo`). Its own rules still apply: integer steps of 1 (`time_gap`), `holdout_from` and lift windows in `field` units.

## Adstock carry-over

A fit that adstocks raw spend in-model (`REG_MMM_BAYES`; `REG_MMM_HILL` with `params.adstock`) records `outputs.adstock_state`: per series, its last `max_lag` fitted periods of each channel's raw spend (`field`, `partition`, `kernel`, `max_lag`, `step`, `series [{key, order, spend}]`); a saved model keeps it. Scoring a later period carries that adstock in instead of restarting at zero, which would bias the first scored periods low.

- **Engine `holdout`** (default): each held-out row's channel reads Σ w_l·spend(period − l·step) at the fit's kernel (its coefficients: `REG_MMM_BAYES` posterior means, population value on a panel; unnormalised), spend from the held-out rows or, for a train period, the state. `params.carryover: false` scores the held-out columns as given (no carry-in); on `REG_MMM_HILL` it refuses the holdout (`adstock_holdout`).
- **Scoring** (`ATTR_REG_PREDICT`) such a fit needs `params.order {field, partition?}`: the frame's RAW spend, adstocked the same way (two-pass). Without it: `order_required`; `carryover: false` scores pre-adstocked columns as given (built with `WIN_DISTRIBUTED_LAG` from `adstock.<ch>`, `normalize: false`).
- **Fitted / residual** (`ATTR_REG_FITTED`, `ATTR_REG_RESIDUAL` with `params.fit`) take the same `carryover` / `order` and refusals: on the fit's own rows, its own fitted values and residuals (Σ r² = its deviance). Leverage / influence / the studentized kinds carry the same way over `REG_MMM_HILL`, the kernel parameters' partials through the carried lags; over `REG_MMM_BAYES` they are `type_unsupported` by design.
- Scored series must be contiguous at the fit's `step`: a repeat or gap is `PROCESSING_REGRESSION_ORDER`. A row without `field` / `partition`, or a null own spend, scores null.
- Score refusals (`PULSE_MODEL_SCORE_INVALID`, predict too): `order_unused`, `order_field_missing`, `order_field_invalid`, `carryover_unused`, `carryover_state_missing` (a file saved before carry-over: refit and save). A spec's `carryover` without in-model adstock is `PROCESSING_CONFIG` `carryover_unused`.

## See

[`regression-model-options`](regression-model-options.md) · [`regression-spec`](regression-spec.md) · [`mmm-workflow`](mmm-workflow.md)
