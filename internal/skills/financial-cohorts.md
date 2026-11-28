---
name: financial-cohorts
description: decimal128 patterns — precision/scale propagation, banker's rounding, divide-by-zero policy, currency conventions, the decimal error codes. Use when a cohort has monetary fields or a response carries PULSE_DECIMAL_OVERFLOW / PULSE_DECIMAL_PRECISION_LOSS / PULSE_DECIMAL_DIVIDE_BY_ZERO.
type: guide
kind: design
applies_to: process, compose, predict, inspect
covers: [decimal128, PULSE_DECIMAL_OVERFLOW, PULSE_DECIMAL_PRECISION_LOSS, PULSE_DECIMAL_DIVIDE_BY_ZERO]
---

# Financial cohorts (decimal128)

`decimal128` is the type for money-movement workloads — up to 38 decimal digits, per-field declared `(precision, scale)`, banker's rounding, no NaN, no infinity, no in-band null sentinel.

## When to pick decimal128

- Currency, billing, accounting, tax, payments, ledger entries.
- Anything reconciling to a downstream system of record (GL, payments rail, exchange).

`f64` is fine for descriptive analytics where last-cent precision is irrelevant. Once the field drives money movement, switch to decimal.

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

Banker's rounding (half-to-even), the unbiased choice for large series. One mode, no per-op override. At scale 0: `0.5 → 0`, `1.5 → 2`, `2.5 → 2`, `-1.5 → -2`, `1.6 → 2`.

## Result scale

A computed decimal carries a scale, not a declared precision: any result past 38 digits is `PULSE_DECIMAL_OVERFLOW`. A division divides at scale `max(…, 4)` (`MinDecimalScale`), banker's rounding.

## Formulas and filters

In the formula attribute and the expression filter a decimal stays exact — `price / cost > 1.2` is decided exactly. Operator scales, `??` / ternary lifting, the numeric built-ins (`abs`, `round`, `max`, `mean`, …), the `float()` escape and the compile refusals: `decimal-formulas`.

## Divide-by-zero

Decimal divide-by-zero raises `PULSE_DECIMAL_DIVIDE_BY_ZERO`. No NaN, no infinity. Guard divisors with a range filter if the data contains zeros.

## Aggregations on decimal fields

An aggregator takes a decimal field iff its manifest `accepts_types` lists `decimal128`; predict and the runtime refuse every other built-in with ONE `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL` error (never a warning), at every aggregation slot<!-- feature: capability:crosstab --> — crosstab cell and margin aggregations included<!-- /feature -->. Registered extension aggregators are exempt (exact values via `DecimalValue`). What a supported one returns follows from the math it needs:

| Kind | Result |
|---|---|
| sum | decimal128 (overflow → error) |
| min / max | decimal128 |
| mean | decimal128 at `mean_scale` = `max(scale, 4)`; f64 fallback + `PULSE_DECIMAL_PRECISION_LOSS` warning on overflow |
| variance | decimal128 at `2 * mean_scale` (f64 fallback, same warning — always when `2 * mean_scale` > 38, i.e. scale ≥ 20) |
| standard deviation | decimal128 at `mean_scale`, banker-rounded sqrt (f64 fallback, same warning) |
| ratio (decimal numerator / denominator params) | f64 |
| counts (rows, distinct values, nulls) | JSON integer, never a decimal string; null count reads the bitmap only |

An empty sum is `"0.00"` (at the field scale), never null; `n == 0` in the components floor is the emptiness signal. Mean, min / max, variance and standard deviation of no value are `null`.

**Not supported** on decimal: order statistics (median, percentiles), standardised and shape moments (z-score, skewness, kurtosis), modes, range.

## Precision-loss path on the mean

Default path accumulates `decimal128` sum and divides by count. If running sum would overflow `decimal128(38)` mid-aggregation: re-run with `f64` accumulators, emit `PULSE_DECIMAL_PRECISION_LOSS` **warning** (not an error): one per aggregation slot, `details {label, aggregation, field, cells}` (`cells` = fallen-back rows / crosstab cells / margins), on every host and arm alike (decimal folds run buffered). The fallen-back figure is a JSON number, not a decimal string. For audited workloads, split or coarsen so the warning never fires.

## Null state

`decimal128` opts into nullability via `Field.Nullable = true` + the per-record bitmap (see `cohort-schema-design`). Every 16-byte pattern is a legitimate value; the bitmap is the sole authority. Importers parse configured null tokens (`""`, `"null"`, `"na"`, `"n/a"`, case-insensitive) into bitmap bits on nullable fields and reject them on non-nullable fields with `PULSE_IMPORT_ROW_ERROR`.

## Filtering

The standard comparison filterers — range, include / exclude, expression — work natively on decimal128 fields. No dedicated filter type.

## Feature operators

Feature transforms are float math: none emits a decimal column, and categorical- or date-only transforms never take a decimal source. Which accept one is the manifest `accepts_types` of each feature (predict and the runtime refuse the rest of a categorical- or date-only transform's types with `PULSE_FIELD_TYPE_UNSUPPORTED`). Feature outputs are NOT auditor-defensible — for decimal precision downstream, aggregate on the original field.

## Importer accepts / rejects

CSV / TSV / NDJSON / JSON-array importers parse decimal strings strictly. Accepted: `"123"`, `"-123.45"`, `"+0.001"`. Rejected: `"$1,234.56"`, `"1 234.56"`, `"1.5e3"`, leading/trailing whitespace. Malformed row → `PULSE_IMPORT_ROW_ERROR`; row is skipped.

## Cross-links

- `cohort-schema-design` — schema layout, null bitmap, shards.
- `aggregation-design` — aggregator slot semantics; per-op decimal support in `op-agg-*`.
- `pulse_errors_lookup` — canonical message + fixups for every error code mentioned here.
