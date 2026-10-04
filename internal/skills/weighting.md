---
name: weighting
description: Row weights on a Request — the request / per-slot weight and the instance default, probability vs frequency kinds, resolution order, invalid-weight exclusion, which operators honour, skip or refuse a weight, the unweighted-base recipe and the weighted floor keys.
type: guide
kind: design
applies_to: process, compose, predict, inspect
covers: [Request, WeightSpec, SlotWeight, weight, sum_weights, n_eff, n_weight_invalid]
requires: [capability:weighting]
---

# Row weighting

Survey and sampled cohorts carry a per-row weight column. Name it once and every weight-aware figure counts each row as `w` rows. A request naming no weight is byte-identical to before.

## Surface

| Where | Shape | Notes |
|---|---|---|
| Request `weight` | `{"field": "wt", "kind": "probability"}` | `kind` ∈ `probability` (default) \| `frequency` |
| per-slot `weight` | `"wt"`, `{field, kind}` or `null` | aggregations (crosstab `cell` and `margin_aggregations[i]` too), `tests`, `post_tests`, regressions, attributes, overlays, `groups`, crosstab axes. Windows have none |
| `pulse.Options.DefaultWeight` | `*types.WeightSpec` | instance default; never reaches facets |

**`null` ≠ absent.** Absent inherits; `"weight": null` opts that ONE slot out. Compose and chain have no top-level weight — each inner Request carries its own.

The field must be an unsigned-integer or float column (`u4`…`u64`, `f32`, `f64`); another type, an empty field or an unknown `kind` is `PROCESSING_CONFIG`, an unknown name `SERVICE_VALIDATION`.

## Kinds

- `probability` (default) — design weights. Ratios, means, shares and quantiles are scale-free (multiply every weight by a constant, nothing moves); totals scale. The floor adds `n_eff`.
- `frequency` — a row stands for `w` identical rows; integer weights reproduce the physically duplicated cohort exactly. A fractional weight is invalid.

## Resolution

Per slot: its own `weight` (`null` ⇒ opted out) → the request `weight` → `Options.DefaultWeight` → none. `pulse_predict` lists every slot under `data.weights[]`: `{slot, operator, field, kind, status, source}`, `status` ∈ `applied | skipped_not_weight_aware | opted_out | none`, `source` ∈ `slot | request | options | none`; the key is absent when nothing names a weight. Predict refuses exactly what the run refuses — predict first.

## Invalid weights

Never coerced. Zero is valid and contributes nothing. Null, negative, NaN / ±Inf and (under `frequency`) non-integer weights drop the row from that slot, counted in its `n_weight_invalid`; the response carries one `PULSE_WEIGHT_INVALID_ROWS` warning per weight column, `details.by_reason {null, negative, nan_inf, non_integer_frequency}`. `--strict` / `Options.Strict` makes it an error.

## What honours a weight

The manifest is the table: an entry with `weight_aware: true` computes a weighted figure (counts become Σw floats; extensions declare it in their `extensions` entry).

| Class | Explicit weight (slot / request) | Only `Options.DefaultWeight` |
|---|---|---|
| `weight_aware: true` | weighted | weighted |
| other aggregators (min / max / distinct-style) | `PROCESSING_CONFIG` | skipped |
| windows | request weight `PROCESSING_CONFIG` | skipped |
| filters, features, row-local attributes, most groupers | skipped | skipped |
| inferential: tests, regressions, reference-distribution attributes, the quantile grouper, confidence bounds, `Inferential` overlays | `PULSE_WEIGHT_UNSUPPORTED` | `PULSE_WEIGHT_UNSUPPORTED` |

- **Weighted inference is not available yet.** Opt each refused slot out with `"weight": null` — under an instance default that means EVERY inferential slot.
- A weight-aware aggregator over a `decimal128` value field: `PULSE_WEIGHT_UNSUPPORTED` under any weight in force; `null` opts out.
- Quantiles follow Hmisc `wtd.quantile`: `frequency` = type 7 on the duplicated rows; `probability` rescaled so Σw = n (scale-invariant).
- <!-- feature: AGG_WEIGHTED_MEAN, AGG_AVERAGE -->`AGG_WEIGHTED_MEAN` is weighted `AGG_AVERAGE`; its `params.weight_field` is slot-weight sugar (absent ⇒ inherits).<!-- /feature -->
- Extensions without `weight_aware`: an aggregator under an explicit weight, or an attribute / test under any weight, is `PULSE_EXTENSION_NOT_WEIGHT_AWARE`.

## Overlays

Share and index overlays read the host payload, so a weighted host gives weighted layers with no setting. An `Inferential` kind refuses when a weight reaches the OVERLAY (its own, the request's, the default); a host weighted only by its own slot weight does not refuse it; `"weight": null` on the spec opts out.<!-- feature: OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z --> Exempt: `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z`, which needs a weighted-moment cell.<!-- /feature -->

<!-- feature: capability:crosstab -->
## Crosstab and the unweighted base

Cells, every margin and every normalization recompute from the WEIGHTED rows on both arms; `CellCounts` and margin counts stay raw row counts. Report the unweighted base beside a weighted metric with an auxiliary that opts out:

```json
{"weight": {"field": "wt"},
 "crosstab": {"rows": [...], "columns": [...],
   "cell": {"type": "<weighted metric>", "field": "spend", "label": "wspend"},
   "margins": {"rows": true, "columns": true, "grand": true},
   "margin_aggregations": [{"type": "<count>", "field": "id", "label": "base", "weight": null}]}}
```

A record reaches an auxiliary margin only if it reached a CELL (`crosstab-margin-aggregations`).
<!-- /feature -->

## Floor keys

A weighted slot's components add `sum_weights` (Σw of contributing rows), `n_eff` (Kish (Σw)²/Σw², `probability` only) and `n_weight_invalid`. Absence of `sum_weights` is the ONLY per-slot "unweighted" signal. `n`, `n_null`, `CellCounts`, margin counts and grouper `total_n` stay raw — read `n_eff`, not `n`, as the effective sample size. A figure undefined under the weights (0/0 when every weight is zero) is `null` on the wire.

<!-- feature: io_format:spss -->
## SPSS suggestion

`pulse_inspect` / `pulse_predict` report `suggested_weight {field, source: "spss_sidecar", kind: "probability"}` from a `.sav` `WEIGHT BY`. It is NEVER applied — name it as `weight` yourself; `WEIGHT BY` replicates cases, so `kind: "frequency"` is usually the faithful reading.
<!-- /feature -->

## See

`response-components` (floor) · `request-envelope` (slot map) · `aggregation-design` · `overlay-system` · `statistical-testing`<!-- feature: capability:crosstab --> · `crosstab-margin-aggregations`<!-- /feature --> · `pulse_errors_lookup` for the `PULSE_WEIGHT_*` codes.
