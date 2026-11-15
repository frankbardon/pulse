```yaml
name: regression-order
description: Fitting a regression's rows in time order — the shared order block {field, partition, gaps}, how the engine sorts and gap-checks the admitted rows before any fit, the duplicate / gap refusals, panels, the models that require an order, and the Bayesian media-mix model's deprecated params.time alias. Topical design; per-model params in atomic op-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [order, row order, time order, order.field, order.partition, gaps, PROCESSING_REGRESSION_ORDER, order_required, panel, params.time]
requires: [capability:regression_order]
```

# Regression order

A model that carries state from one row to the next — an adstock, a lag, a state-space filter — needs its rows in time order, contiguous. `order` is the one place a regression says so; the engine enforces it before any fit.

## The block

`"order": {"field": "week", "partition": "market", "gaps": "refuse"}` on any regression entry.

| Key | Meaning |
|---|---|
| `field` | (required) The ordering field: an unsigned integer period index, a `date` (days) or a `datetime` (seconds); a cohort field or a derived column. |
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

## See

[`regression-model-options`](regression-model-options.md) · [`regression-spec`](regression-spec.md) · [`mmm-workflow`](mmm-workflow.md)
