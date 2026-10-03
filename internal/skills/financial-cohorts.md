---
name: financial-cohorts
description: decimal128 patterns — precision/scale propagation, banker's rounding, divide-by-zero policy, currency conventions, three consumer-facing decimal error codes. Use when a cohort has monetary fields or a response carries PULSE_DECIMAL_OVERFLOW / PULSE_PRECISION_LOSS / PULSE_DECIMAL_DIVIDE_BY_ZERO.
type: guide
kind: design
applies_to: process, compose, predict, inspect
covers: [decimal128, PULSE_DECIMAL_OVERFLOW, PULSE_PRECISION_LOSS, PULSE_DECIMAL_DIVIDE_BY_ZERO]
---

# Financial cohorts (decimal128)

`decimal128` is the only defensible type for money-movement workloads — up to 38 decimal digits, per-field declared `(precision, scale)`, banker's rounding, no NaN, no infinity, no in-band null sentinel.

## When to pick decimal128

- Currency, billing, accounting, tax, payments, ledger entries.
- Anything reconciling to a downstream system of record (GL, payments rail, exchange).
- Cohorts auditors will read.

`f64` is fine for descriptive analytics where last-cent precision is irrelevant. Once the column drives money movement, switch to decimal.

## Type spec

`decimal128(precision, scale)` — precision = total significant digits (1–38); scale = digits after the decimal point (0–precision).

| Type | Use for |
|---|---|
| `decimal128(20, 6)` | USD ledger with sub-penny accumulation |
| `decimal128(18, 2)` | Standard USD / EUR amounts |
| `decimal128(38, 18)` | Crypto native units |
| `decimal128(38, 0)` | Integer-only counts beyond u64 |

Both precision and scale persist in the schema; `pulse_inspect` surfaces both per field.

## Rounding

Banker's rounding (half-to-even). One mode, no per-op override. IEEE 754 default; the only unbiased choice for large-series aggregation. At scale 0: `0.5 → 0`, `1.5 → 2`, `2.5 → 2`, `-1.5 → -2`, `1.6 → 2`.

## Precision propagation (SQL:2016 / Arrow Decimal128)

| Op | Result precision | Result scale |
|---|---|---|
| `(p1,s1) + (p2,s2)` | `max(p1-s1, p2-s2) + max(s1,s2) + 1` | `max(s1, s2)` |
| `(p1,s1) - (p2,s2)` | same as `+` | same as `+` |
| `(p1,s1) * (p2,s2)` | `p1 + p2` | `s1 + s2` |
| `(p1,s1) / (p2,s2)` | `p1 + s2 + 1` | `max(s1+s2, MIN_SCALE)` where `MIN_SCALE = 4` |

All result precisions clamp at **38**. When clamping kicks in and the value overflows, `PULSE_DECIMAL_OVERFLOW`.

Examples: `(10,4) + (10,4) → (11,4)`. `(10,4) * (10,4) → (20,8)`. `(10,4) / (10,4) → (15,8)`.

## Mixed-type arithmetic

No implicit cast between `decimal128` and `f64`. Mixing them in a filter expression or a formula raises a type error (`expression-language`). Downcast via an explicit attribute.

## Divide-by-zero

Decimal divide-by-zero raises `PULSE_DECIMAL_DIVIDE_BY_ZERO`. No NaN, no infinity. Guard divisors with a range filter if the data contains zeros.

## Aggregations on decimal fields

An aggregator takes a decimal field iff its manifest `accepts_types` lists `decimal128`; predict refuses the rest with `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`. Registered extension aggregators are exempt (exact values via `DecimalValue`). What a supported one returns follows from the math it needs:

| Kind | Result |
|---|---|
| sum | decimal128 (overflow → error) |
| min / max | decimal128 |
| mean | decimal128; f64 fallback + `PULSE_PRECISION_LOSS` warning on overflow |
| variance | decimal128 at `2 * mean_scale` (f64 fallback) |
| standard deviation | decimal128 at `mean_scale`, banker-rounded sqrt (f64 fallback) |
| counts (rows, distinct values) | int |

**v1 not supported** on decimal: order statistics (median, percentiles), standardised and shape moments (z-score, skewness, kurtosis), modes, range.

## Precision-loss path on the mean

Default path accumulates `decimal128` sum and divides by count. If running sum would overflow `decimal128(38)` mid-aggregation: re-run with `f64` accumulators, emit `PULSE_PRECISION_LOSS` **warning** (not an error). For audited workloads, split or coarsen so the warning never fires.

## Null state

`decimal128` opts into nullability via `Field.Nullable = true` + the per-record bitmap (see `cohort-schema-design`). Every 16-byte pattern is a legitimate value; the bitmap is the sole authority. Importers parse configured null tokens (`""`, `"null"`, `"na"`, `"n/a"`, case-insensitive) into bitmap bits on nullable fields and reject them on non-nullable fields with `PULSE_IMPORT_ROW_ERROR`.

## Filtering

The standard comparison filterers — range, include / exclude, expression — work natively on decimal128 fields. No dedicated filter type.

## Feature operators

Feature transforms are float math: none emits a decimal column, and categorical- or date-only transforms never take a decimal source. Which accept one is the manifest `accepts_types` of each feature (predict refuses the rest with `SERVICE_VALIDATION`). Feature outputs are NOT auditor-defensible — for decimal precision downstream, aggregate on the original column.

## Importer accepts / rejects

CSV / TSV / NDJSON / JSON-array importers parse decimal strings strictly. Accepted: `"123"`, `"-123.45"`, `"+0.001"`. Rejected: `"$1,234.56"`, `"1 234.56"`, `"1.5e3"`, leading/trailing whitespace. Malformed row → `PULSE_IMPORT_ROW_ERROR`; row is skipped.

## Request shape skeleton

<!-- feature: AGG_SUM, AGG_AVERAGE -->
```
{
  "aggregations": [
    {"type": "AGG_SUM",     "field": "amount_usd"},
    {"type": "AGG_AVERAGE", "field": "amount_usd"}
  ]
}
```
<!-- /feature -->

The sum stays in `decimal128(p, s)`. The mean preserves precision by default; large cohorts may fall back to f64 with a `PULSE_PRECISION_LOSS` warning.

## v1 deferred

- Per-op rounding-mode override.
- `MIN_SCALE` configuration on division (locked at 4).
- Decimal-aware median / percentile / mode.

## Cross-links

- `cohort-schema-design` — schema layout, null bitmap, shards.
- `aggregation-design` — aggregator slot semantics; per-op decimal support in `op-agg-*`.
- `pulse_errors_lookup` — canonical message + fixups for every error code mentioned here.
