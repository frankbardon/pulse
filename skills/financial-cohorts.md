---
name: financial-cohorts
description: Decimal128 usage, precision propagation, banker's rounding, currency-column patterns, divide-by-zero policy
type: guide
applies_to: process, compose, predict, inspect
---

# Financial Cohorts (decimal128)

<skill_overview>
Pulse's `decimal128` type is the only type defensible for money-movement workloads. It stores up to 38 decimal digits with a per-field declared precision and scale. This skill pins the rules: when to pick decimal vs f64, how precision propagates through arithmetic, how rounding behaves, what overflow looks like, and the v1 limits.
</skill_overview>

## When to use decimal128

Pick `decimal128` whenever **silent loss of precision is unacceptable**. Concrete triggers:

- Currency, billing, accounting, tax, payments, ledger entries.
- Anything that reconciles to a downstream system of record (an accounting GL, a payments rail, an exchange).
- Cohorts that auditors will read.

`f64` is a fine choice for descriptive analytics on revenue or volumes where last-cent precision does not matter. But once the same column drives money movement, switch to decimal.

## Type spec: `decimal128(precision, scale)`

- **Precision**: total significant digits (1–38).
- **Scale**: digits after the decimal point (0–precision).

Examples:

| Type | Range | Use for |
|---|---|---|
| `decimal128(20, 6)` | ±99,999,999,999,999.999999 | USD ledgers with sub-penny accumulation |
| `decimal128(18, 2)` | ±9,999,999,999,999,999.99 | Standard USD/EUR amounts |
| `decimal128(38, 18)` | ±99,999,999,999,999,999,999.999999999999999999 | Crypto, ETH/BTC at native unit |
| `decimal128(38, 0)` | ±10^38 - 1 | Integer-only counts that need 128-bit range |

Precision and scale are **per field**, declared in the schema and persisted in the .pulse header. `pulse inspect` surfaces both.

## Rounding mode

Pulse uses **banker's rounding** (half-to-even) — one mode, no per-op override. This is the IEEE 754 default and the only safe choice for unbiased aggregation of large series.

Examples at scale 0:

| Input | Rounded |
|---|---|
| `0.5` | `0` |
| `1.5` | `2` |
| `2.5` | `2` |
| `-1.5` | `-2` |
| `1.6` | `2` |
| `1.4` | `1` |

Banker's rounding rounds half-values toward the nearest **even** integer. Repeated applications over millions of rows do not introduce bias the way "round half up" would.

## Precision propagation rules (SQL:2016 / Arrow Decimal128)

| Operation | Result precision | Result scale |
|---|---|---|
| `(p1, s1) + (p2, s2)` | `max(p1-s1, p2-s2) + max(s1, s2) + 1` | `max(s1, s2)` |
| `(p1, s1) - (p2, s2)` | same as `+` | same as `+` |
| `(p1, s1) * (p2, s2)` | `p1 + p2` | `s1 + s2` |
| `(p1, s1) / (p2, s2)` | `p1 + s2 + 1` | `max(s1+s2, MIN_SCALE)` where `MIN_SCALE = 4` |

All result precisions are clamped at **38**. When clamping kicks in and the actual value overflows, Pulse emits `PULSE_DECIMAL_OVERFLOW`.

### Worked example

`decimal128(10, 4) + decimal128(10, 4) → decimal128(11, 4)`
`decimal128(10, 4) * decimal128(10, 4) → decimal128(20, 8)`
`decimal128(10, 4) / decimal128(10, 4) → decimal128(15, 8)` (with `MIN_SCALE=4`)

## Mixed-type arithmetic

There is no implicit cast between `decimal128` and `f64`. Mixing them in a `FILTER_EXPRESSION` or `ATTR_FORMULA` triggers a type error. To downcast, use an attribute that explicitly converts.

## Divide-by-zero

Decimal divide by zero **errors with `PULSE_DECIMAL_DIVIDE_BY_ZERO`**. There is no NaN, no infinity. Guard divisors with a `FILTER_RANGE` if your data contains zeros.

## Aggregations on decimal fields

| Aggregator | Defined on decimal? | Result type |
|---|---|---|
| `AGG_SUM` | yes | `decimal128` (overflow → error) |
| `AGG_AVERAGE` | yes | `decimal128` (overflow → f64 fallback + warning) |
| `AGG_MIN` | yes | `decimal128` |
| `AGG_MAX` | yes | `decimal128` |
| `AGG_VARIANCE` | yes (f64 result) | `f64` |
| `AGG_STDDEV` | yes (f64 result) | `f64` |
| `AGG_COUNT` | yes | int |
| `AGG_DISTINCT_COUNT` | yes | int |
| `AGG_MEDIAN` | **no** v1 | — |
| `AGG_PERCENTILE` | **no** v1 | — |
| `AGG_ZSCORE` | **no** v1 | — |
| `AGG_SKEWNESS` / `AGG_KURTOSIS` | **no** v1 | — |
| `AGG_MODE` / `AGG_FREQUENCY` / `AGG_RANGE` | **no** v1 | — |

Predict reports `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL` for any aggregation outside the supported set.

## AGG_AVERAGE and the precision-loss path

`AGG_AVERAGE` preserves precision by default — the implementation accumulates the sum as `decimal128` and divides by the count. When the running sum would overflow `decimal128(38)` mid-aggregation, Pulse:

1. Re-runs the aggregation with `f64` accumulators.
2. Emits a `PULSE_DECIMAL_PRECISION_LOSS` **warning** (not an error).

For audited workloads, split or coarsen so the warning never fires. For exploratory analytics, the warning is informational.

## Importer rules

The CSV/TSV/NDJSON/JSON-array importers parse decimal strings strictly:

- **Accepted**: `"123"`, `"-123.45"`, `"+0.001"`.
- **Rejected**: `"$1,234.56"`, `"1 234.56"`, `"1.5e3"`, leading or trailing whitespace.

Fail-closed: a malformed row produces `PULSE_IMPORT_ROW_ERROR` and skips the row.

## Filtering decimal fields

The standard comparison filterers (`FILTER_RANGE`, `FILTER_INCLUDE`, `FILTER_EXCLUDE`, `FILTER_EXPRESSION`) work natively on decimal128 fields. No new filter type is needed.

## NULL: `nullable_decimal128`

The `nullable_decimal128` type carries an extra null sentinel value: bit pattern `INT128_MIN` (`0x80` in the high byte, all other bytes zero). The importer rejects this exact value as a legitimate input — if your source data legitimately contains it, scale or shift before import.

## v1 deferred items

- Per-op rounding mode override.
- `MIN_SCALE` configuration on division (locked at 4 in v1).
- Decimal-aware median / percentile / mode.
- Decimal results of `AGG_VARIANCE` / `AGG_STDDEV` (currently f64).
- `FEAT_LOG`, `FEAT_SQRT`, etc. on decimal — currently reject; cast to f64 first.

## Common patterns

### A USD ledger with sub-penny accumulation

```json
{
  "fields": [
    {"name": "amount_usd", "type": "decimal128", "precision": 20, "scale": 6,
     "description": "Amount in USD with micro-cent resolution; positive credit, negative debit."}
  ]
}
```

### Sum + average over an account

```json
{
  "filterers": [{"type": "FILTER_INCLUDE", "field": "account_id", "values": ["A123"]}],
  "aggregations": [
    {"type": "AGG_SUM",     "field": "amount_usd"},
    {"type": "AGG_AVERAGE", "field": "amount_usd"}
  ]
}
```

The SUM result is `decimal128(20, 6)`. The AVERAGE result preserves precision by default; if the cohort is large enough that the running sum would overflow, the response carries a `PULSE_DECIMAL_PRECISION_LOSS` warning and the result is `f64`.
